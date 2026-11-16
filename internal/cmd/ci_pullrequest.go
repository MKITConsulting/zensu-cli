package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/spf13/cobra"
)

type pullRequestUpload struct {
	ProductID  string  `json:"productId"`
	PRNumber   int64   `json:"prNumber"`
	Title      string  `json:"title"`
	Body       *string `json:"body,omitempty"`
	PRURL      *string `json:"prUrl,omitempty"`
	Status     *string `json:"status,omitempty"`
	Repository *string `json:"repository,omitempty"`
	BaseRef    *string `json:"baseRef,omitempty"`
	HeadSHA    *string `json:"headSha,omitempty"`
	MergeSHA   *string `json:"mergeSha,omitempty"`
}

type pullRequestAnswer struct {
	PRNumber float64 `json:"prNumber"`
	Linked   []struct {
		Ref string `json:"ref"`
	} `json:"linked"`
	Unresolved          []string `json:"unresolved"`
	WorkOrderMergeError string   `json:"workOrderMergeError"`
}

var pullRequestAnswerFields = []answerField{
	{"prNumber", jsonNumber},
	{"linked", jsonArray},
	{"unresolved", jsonArray},
}

var pullRequestStatuses = []string{"open", "closed", "merged"}

type pullRequestFlags struct {
	number     int64
	title      string
	url        string
	status     string
	body       string
	repository string
	baseRef    string
	headSHA    string
	mergeSHA   string
}

func newCIPullRequestCmd(f *Factory) *cobra.Command {
	var flags ciFlags
	var pr pullRequestFlags
	cmd := &cobra.Command{
		Use:   "pull-request",
		Short: "Link a pull request to the features it references",
		Long: "Upload a pull request so Zensu links it to every feature its title and body reference, such as [ZEN-42].\n\n" +
			"With --github the pull request comes from the pull_request event: number, title, body, URL, state " +
			"(merged, closed or open), base branch, head commit and, for a merged pull request, its merge commit; the " +
			"repository is GITHUB_SERVER_URL/GITHUB_REPOSITORY. Without --github pass at least --number and --title. " +
			"Explicit flags always win.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if path, _ := cmd.Flags().GetString("body-file"); flags.apiKeyStdin && path == "-" {
				return errors.New("--api-key-stdin and --body-file - both read stdin; pass --body-file a file path")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			run, err := flags.start(cmd, f)
			if err != nil {
				return err
			}
			upload, err := run.pullRequest(cmd, pr)
			if err != nil {
				return err
			}
			return run.uploadPullRequest(cmd.Context(), upload)
		},
	}
	flags.register(cmd)
	cmd.Flags().Int64Var(&pr.number, "number", 0, "pull request number (required unless --github)")
	cmd.Flags().StringVar(&pr.title, "title", "", "pull request title; its references link features (required unless --github)")
	cmd.Flags().StringVar(&pr.url, "url", "", "web URL of the pull request")
	cmd.Flags().StringVar(&pr.status, "status", "", "open, closed or merged")
	cmd.Flags().StringVar(&pr.body, "body", "", "pull request description (Markdown); its references link features too")
	cmd.Flags().StringVar(&pr.repository, "repository", "", "web URL of the repository, such as https://github.com/acme/shop")
	cmd.Flags().StringVar(&pr.baseRef, "base-ref", "", "branch the pull request merges into")
	cmd.Flags().StringVar(&pr.headSHA, "head-sha", "", "head commit of the pull request")
	cmd.Flags().StringVar(&pr.mergeSHA, "merge-sha", "", "merge commit of a merged pull request")
	return withValueFiles(cmd, textFile("body"))
}

func (r *ciRun) pullRequest(cmd *cobra.Command, pr pullRequestFlags) (pullRequestUpload, error) {
	upload := pullRequestUpload{ProductID: r.flags.product}
	var body string
	if r.flags.github {
		event, err := readGitHubEvent()
		if err != nil {
			return upload, err
		}
		if pull, ok := nestedValue(event, "pull_request").(map[string]any); ok {
			body = r.applyGitHubPullRequest(&upload, pull)
		}
		if repository := githubRepositoryURL(); repository != "" {
			upload.Repository = &repository
		}
	}
	changed := cmd.Flags().Changed
	if changed("number") {
		upload.PRNumber = pr.number
	}
	if changed("title") {
		upload.Title = pr.title
	}
	if changed("body") {
		body = pr.body
	}
	if changed("status") {
		if !slices.Contains(pullRequestStatuses, pr.status) {
			return upload, fmt.Errorf("--status must be open, closed or merged, got %q", sanitizeTerminal(pr.status))
		}
		upload.Status = &pr.status
	}
	for flag, field := range map[string]**string{
		"url":        &upload.PRURL,
		"repository": &upload.Repository,
		"base-ref":   &upload.BaseRef,
		"head-sha":   &upload.HeadSHA,
		"merge-sha":  &upload.MergeSHA,
	} {
		if changed(flag) {
			value, _ := cmd.Flags().GetString(flag)
			*field = optionalString(value)
		}
	}
	if body != "" {
		upload.Body = &body
	}
	if upload.PRNumber <= 0 || upload.PRNumber > math.MaxInt32 {
		if upload.PRNumber == 0 {
			return upload, errors.New("--number is required unless --github reads a pull_request event")
		}
		return upload, fmt.Errorf("pull request number %d is out of range", upload.PRNumber)
	}
	if strings.TrimSpace(upload.Title) == "" {
		return upload, errors.New("--title is required unless --github reads a pull_request event")
	}
	return upload, nil
}

func (r *ciRun) applyGitHubPullRequest(upload *pullRequestUpload, pull map[string]any) string {
	if number, ok := pull["number"].(json.Number); ok {
		if n, err := number.Int64(); err == nil {
			upload.PRNumber = n
		}
	}
	upload.Title, _ = pull["title"].(string)
	upload.PRURL = optionalString(eventString(pull, "html_url"))
	upload.BaseRef = optionalString(eventString(pull, "base", "ref"))
	upload.HeadSHA = optionalString(eventString(pull, "head", "sha"))
	merged := pull["merged"] == true
	status := "open"
	switch {
	case merged:
		status = "merged"
	case pull["state"] == "closed":
		status = "closed"
	}
	upload.Status = &status
	if merged {
		upload.MergeSHA = optionalString(eventString(pull, "merge_commit_sha"))
	}
	body, _ := pull["body"].(string)
	return body
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (r *ciRun) uploadPullRequest(ctx context.Context, upload pullRequestUpload) error {
	var answer pullRequestAnswer
	what := fmt.Sprintf("upload pull request #%d", upload.PRNumber)
	raw, err := r.post(ctx, "/api/ci/pull-requests", what, upload, pullRequestAnswerFields, &answer)
	if err != nil {
		return err
	}
	number := formatJSONNumber(answer.PRNumber)
	if r.flags.asJSON {
		err = printJSON(r.report.out, raw)
	} else {
		err = r.reportPullRequest(upload, answer, number)
	}
	if answer.WorkOrderMergeError != "" {
		r.report.warn("Zensu kept pull request #%s but did not record its merge evidence: %s", number, sanitizeTerminal(answer.WorkOrderMergeError))
	}
	return err
}

func (r *ciRun) reportPullRequest(upload pullRequestUpload, answer pullRequestAnswer, number string) error {
	var err error
	if len(answer.Linked) > 0 {
		refs := make([]string, 0, len(answer.Linked))
		for _, linked := range answer.Linked {
			refs = append(refs, sanitizeTerminal(linked.Ref))
		}
		err = r.report.say("Zensu linked pull request #%s to %s.", number, strings.Join(refs, ", "))
	} else {
		err = r.report.say("Pull request #%s references no feature of this product.", number)
	}
	if err != nil {
		return err
	}
	text := upload.Title + " "
	if upload.Body != nil {
		text += *upload.Body
	}
	var unmatched []string
	for _, m := range featureReferencePattern.FindAllStringSubmatch(text, -1) {
		if slices.Contains(answer.Unresolved, m[2]) {
			unmatched = append(unmatched, strings.ToUpper(m[1])+"-"+m[2])
		}
	}
	slices.Sort(unmatched)
	for _, ref := range slices.Compact(unmatched) {
		if err := r.report.say("No feature of this product matches %s.", ref); err != nil {
			return err
		}
	}
	return nil
}
