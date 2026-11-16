package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	ciAPIKeyMaxBytes = 4 << 10
	ciEventMaxBytes  = 64 << 20
)

const githubAnnotationsAnnotation = "zensu/github-annotations"

var featureReferencePattern = regexp.MustCompile(`\[([A-Za-z][A-Za-z0-9]*)-([0-9A-Za-z_-]+)\]`)

func NewCICmd(f *Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Upload coverage, commits and pull requests from a CI pipeline",
		Long: "Upload what a CI run knows to Zensu: per-file coverage, the commits whose messages reference a feature " +
			"such as [ZEN-42], and the pull request. The API key needs the ci scope (the CI pipeline key profile); keys " +
			"with the write scope work too.\n\n" +
			"--api-key-stdin reads the key from stdin and uses only it: the stored login is neither read nor written, " +
			"and the API host must come from --api-url or ZENSU_API_URL. --github takes the run context from the GitHub " +
			"Actions environment and prints warnings and errors as workflow annotations; explicit flags still win.",
	}
	cmd.AddCommand(newCICoverageCmd(f), newCICommitsCmd(f), newCIPullRequestCmd(f))
	return cmd
}

type ciFlags struct {
	product     string
	github      bool
	apiKeyStdin bool
	asJSON      bool
}

func (o *ciFlags) register(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[githubAnnotationsAnnotation] = "true"
	cmd.Flags().StringVar(&o.product, "product", "", "product ID (required)")
	cmd.Flags().BoolVar(&o.github, "github", false, "take the run context from the GitHub Actions environment and print warnings and errors as workflow annotations")
	cmd.Flags().BoolVar(&o.apiKeyStdin, "api-key-stdin", false, "read the API key from stdin instead of using the stored login; needs --api-url or ZENSU_API_URL")
	cmd.Flags().BoolVar(&o.asJSON, "json", false, "output raw JSON")
}

type ciRun struct {
	flags  *ciFlags
	sender *Factory
	report ciReporter
}

func (o *ciFlags) start(cmd *cobra.Command, f *Factory) (*ciRun, error) {
	if o.product == "" {
		return nil, errors.New("--product is required")
	}
	if err := requireUUIDFlag("product", o.product); err != nil {
		return nil, err
	}
	sender := f
	if o.apiKeyStdin {
		var err error
		if sender, err = stdinKeyFactory(cmd, f); err != nil {
			return nil, err
		}
	}
	return &ciRun{
		flags:  o,
		sender: sender,
		report: ciReporter{out: f.Out, errOut: cmd.ErrOrStderr(), github: o.github},
	}, nil
}

func stdinKeyFactory(cmd *cobra.Command, f *Factory) (*Factory, error) {
	apiURL := ""
	if flag := cmd.Flags().Lookup("api-url"); flag != nil {
		apiURL = flag.Value.String()
	}
	if apiURL == "" {
		apiURL = os.Getenv("ZENSU_API_URL")
	}
	if apiURL == "" {
		return nil, fmt.Errorf("--api-key-stdin needs the API host: pass --api-url or set ZENSU_API_URL; the key is never sent to the built-in default %s", config.DefaultAPIURL)
	}
	key, err := readStdinKey(cmd.InOrStdin())
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(apiURL, "/")
	return &Factory{
		Out: f.Out,
		NewClient: func(context.Context) (*client.Client, error) {
			return client.New(&config.Config{APIKey: key}, base, ""), nil
		},
	}, nil
}

func readStdinKey(stdin io.Reader) (string, error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, ciAPIKeyMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("--api-key-stdin: reading stdin: %w", err)
	}
	if len(raw) > ciAPIKeyMaxBytes {
		return "", errors.New("--api-key-stdin: stdin holds more than 4 KiB, which is no API key")
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", errors.New("--api-key-stdin read no API key from stdin")
	}
	if strings.IndexFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) >= 0 {
		return "", errors.New("--api-key-stdin: the key on stdin contains spaces or control characters; pipe the key alone")
	}
	return key, nil
}

type ciReporter struct {
	out    io.Writer
	errOut io.Writer
	github bool
}

func (r ciReporter) say(format string, args ...any) error {
	_, err := fmt.Fprintf(r.out, format+"\n", args...)
	return err
}

func (r ciReporter) warn(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if r.github {
		fmt.Fprintln(r.errOut, workflowCommand("warning", message))
		return
	}
	fmt.Fprintln(r.errOut, "warning: "+message)
}

var workflowDataEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")

func workflowCommand(kind, message string) string {
	return "::" + kind + "::" + workflowDataEscaper.Replace(message)
}

func annotatesErrors(ran *cobra.Command, args []string, err error) bool {
	if ran == nil || ran.Annotations[githubAnnotationsAnnotation] != "true" {
		return false
	}
	if flag := ran.Flags().Lookup("github"); flag != nil {
		if on, _ := strconv.ParseBool(flag.Value.String()); on {
			return true
		}
	}
	var parseErr *flagParseError
	return errors.As(err, &parseErr) && argsRequestFlag(args, "github")
}

type jsonKind int

const (
	jsonOther jsonKind = iota
	jsonNumber
	jsonArray
)

type answerField struct {
	name string
	kind jsonKind
}

func (r *ciRun) post(ctx context.Context, path, what string, payload any, required []answerField, answer any) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	c, err := r.sender.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, http.MethodPost, path, body)
	redact := newCredentialRedactor(sentCredentials(resp))
	raw, err := readResponse(resp, err)
	if err != nil {
		return nil, err
	}
	if !isUploadAnswer(raw, required, answer) {
		return nil, fmt.Errorf("the answer to %s is not a Zensu upload result; check that the API URL points at the Zensu API (answer: %s)", what, answerExcerpt(raw, redact))
	}
	return raw, nil
}

func isUploadAnswer(raw []byte, required []answerField, answer any) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if dec.Decode(&fields) != nil || fields == nil {
		return false
	}
	var extra json.RawMessage
	if !errors.Is(dec.Decode(&extra), io.EOF) {
		return false
	}
	for _, field := range required {
		value, ok := fields[field.name]
		if !ok || kindOf(value) != field.kind {
			return false
		}
	}
	return json.Unmarshal(raw, answer) == nil
}

func kindOf(value json.RawMessage) jsonKind {
	trimmed := bytes.TrimLeft(value, " \t\r\n")
	if len(trimmed) == 0 {
		return jsonOther
	}
	switch c := trimmed[0]; {
	case c == '[':
		return jsonArray
	case c == '-' || (c >= '0' && c <= '9'):
		return jsonNumber
	}
	return jsonOther
}

func answerExcerpt(raw []byte, redact credentialRedactor) string {
	text := strings.TrimSpace(sanitizeTerminal(excerpt(redact.text(string(raw)))))
	if text == "" {
		return "empty"
	}
	return text
}

func readGitHubEvent() (map[string]any, error) {
	path := os.Getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return nil, nil
	}
	file, err := openValueFile(path)
	if err != nil {
		return nil, nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(file, ciEventMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the GitHub event %s: %w", path, err)
	}
	if len(data) > ciEventMaxBytes {
		return nil, fmt.Errorf("the GitHub event %s exceeds %s", path, byteSize(ciEventMaxBytes))
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var event map[string]any
	if err := dec.Decode(&event); err != nil {
		return nil, fmt.Errorf("the GitHub event %s is not a JSON object: %w", path, err)
	}
	return event, nil
}

func nestedValue(object map[string]any, keys ...string) any {
	var v any = object
	for _, key := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[key]
	}
	return v
}

func eventString(event map[string]any, keys ...string) string {
	s, _ := nestedValue(event, keys...).(string)
	return s
}

func runGit(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(sanitizeMultiline(stderr.String()))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", args[0], detail)
	}
	return out, nil
}

func githubRepositoryURL() string {
	server, repository := os.Getenv("GITHUB_SERVER_URL"), os.Getenv("GITHUB_REPOSITORY")
	if server == "" || repository == "" {
		return ""
	}
	return strings.TrimSuffix(server, "/") + "/" + repository
}

func formatJSONNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
