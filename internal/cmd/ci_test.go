package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

const ciTestProduct = "7c1e7d4a-2b9f-4c51-9d3e-8a6f5b4c3d21"

type ciResult struct {
	stdout string
	stderr string
	err    error
}

func (r ciResult) lines() []string {
	return strings.Split(strings.TrimRight(r.stdout+r.stderr, "\n"), "\n")
}

func runCI(t *testing.T, f *Factory, stdin string, args ...string) ciResult {
	t.Helper()
	root := &cobra.Command{Use: "zensu", SilenceErrors: true, SilenceUsage: true}
	root.SetFlagErrorFunc(markFlagParseError)
	root.PersistentFlags().String("api-url", "", "Zensu API base URL")
	root.AddCommand(NewCICmd(f))
	var stderr bytes.Buffer
	root.SetIn(strings.NewReader(stdin))
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"ci"}, args...))
	ran, err := root.ExecuteC()
	WriteError(&stderr, ran, append([]string{"ci"}, args...), err)
	out, _ := f.Out.(*bytes.Buffer)
	return ciResult{stdout: out.String(), stderr: stderr.String(), err: err}
}

func unusedLoginFactory(t *testing.T) *Factory {
	t.Helper()
	return &Factory{
		Out: &bytes.Buffer{},
		NewClient: func(context.Context) (*client.Client, error) {
			t.Error("the stored login was used")
			return nil, os.ErrInvalid
		},
	}
}

func requireJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v\n%s", err, want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("JSON differs\n got: %s\nwant: %s", got, want)
	}
}

func writeTestFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func clearGitHubEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GITHUB_ACTIONS", "GITHUB_EVENT_NAME", "GITHUB_EVENT_PATH", "GITHUB_SHA", "GITHUB_WORKSPACE", "GITHUB_SERVER_URL", "GITHUB_REPOSITORY", "GITHUB_RUN_ID", "GITHUB_STEP_SUMMARY", "ZENSU_API_URL", sessionTokenEnv} {
		t.Setenv(name, "")
	}
}

func commitsReply(r recordedRequest) fakeReply {
	var req struct {
		Commits []struct {
			Hash    string `json:"hash"`
			Message string `json:"message"`
		} `json:"commits"`
	}
	_ = json.Unmarshal([]byte(r.Raw), &req)
	links := []map[string]any{}
	for _, c := range req.Commits {
		refs, unresolved := []string{}, []string{}
		linked := 0
		for _, m := range featureReferencePattern.FindAllStringSubmatch(c.Message, -1) {
			refs = append(refs, strings.ToUpper(m[1])+"-"+m[2])
			if m[2] == "42" || m[2] == "7" || m[2] == "invoice-rounding" {
				linked++
			} else {
				unresolved = append(unresolved, m[2])
			}
		}
		links = append(links, map[string]any{"commitHash": c.Hash, "featureSlugs": []string{}, "featureRefs": refs, "linked": linked, "unresolved": unresolved})
	}
	return fakeReply{Body: map[string]any{"processed": len(req.Commits), "links": links}}
}

func TestCI_APIKeyStdinSendsOnlyTheKeyFromStdin(t *testing.T) {
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	repo.commit("feat: rotate tokens [ZEN-42]")
	t.Chdir(repo.dir)
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)
	stored := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the stored host was called")
	}))
	defer stored.Close()
	storeLogin(t, config.Config{APIURL: stored.URL, APIKey: "zsk_stored_EXAMPLE"})
	before, err := os.ReadFile(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries := configEntries(t, dir)
	t.Setenv(sessionTokenEnv, "zst_EXAMPLEsessiontokenEXAMPLEsessiontoken0123")
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST /api/ci/commits": commitsReply})

	res := runCI(t, unusedLoginFactory(t), "  zsk_EXAMPLE_from_stdin\n", "commits", "--product", ciTestProduct, "--api-key-stdin", "--api-url", srv.URL+"/")

	if res.err != nil {
		t.Fatalf("commits: %v\n%s", res.err, res.stderr)
	}
	call := api.only(t, http.MethodPost, "/api/ci/commits")
	if call.APIKey != "zsk_EXAMPLE_from_stdin" || call.Auth != "" {
		t.Fatalf("X-API-Key = %q Authorization = %q, want only the key from stdin", call.APIKey, call.Auth)
	}
	after, err := os.ReadFile(filepath.Join(dir, "hosts.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the stored login changed (%v)", err)
	}
	if afterEntries := configEntries(t, dir); !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatalf("config dir went from %v to %v", beforeEntries, afterEntries)
	}
}

func configEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestCI_APIKeyStdinReadsTheHostFromZENSU_API_URL(t *testing.T) {
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	repo.commit("feat: rotate tokens [ZEN-42]")
	t.Chdir(repo.dir)
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST /api/ci/commits": commitsReply})
	t.Setenv("ZENSU_API_URL", srv.URL+"/")

	res := runCI(t, unusedLoginFactory(t), "zsk_EXAMPLE_from_stdin", "commits", "--product", ciTestProduct, "--api-key-stdin")

	if res.err != nil {
		t.Fatalf("commits: %v", res.err)
	}
	if call := api.only(t, http.MethodPost, "/api/ci/commits"); call.APIKey != "zsk_EXAMPLE_from_stdin" {
		t.Fatalf("X-API-Key = %q", call.APIKey)
	}
}

func TestCI_APIKeyStdinNeedsAnExplicitHost(t *testing.T) {
	clearGitHubEnv(t)
	storeLogin(t, config.Config{APIURL: "http://127.0.0.1:1", APIKey: "zsk_stored_EXAMPLE"})
	res := runCI(t, unusedLoginFactory(t), "zsk_EXAMPLE_from_stdin", "commits", "--product", ciTestProduct, "--api-key-stdin")
	want := "--api-key-stdin needs the API host: pass --api-url or set ZENSU_API_URL; the key is never sent to the built-in default " + config.DefaultAPIURL
	if res.err == nil || res.err.Error() != want {
		t.Fatalf("error = %v, want %q", res.err, want)
	}
	if res.stdout != "" || res.stderr != "error: "+want+"\n" {
		t.Fatalf("stdout = %q stderr = %q", res.stdout, res.stderr)
	}
}

func TestCI_APIKeyStdinRefusesAMissingOrMalformedKey(t *testing.T) {
	clearGitHubEnv(t)
	for _, tc := range []struct {
		stdin string
		want  string
	}{
		{"", "--api-key-stdin read no API key from stdin"},
		{" \n\t", "--api-key-stdin read no API key from stdin"},
		{"zsk_EXAMPLE one", "--api-key-stdin: the key on stdin contains spaces or control characters; pipe the key alone"},
		{"zsk_EXAMPLE\x1b[2J", "--api-key-stdin: the key on stdin contains spaces or control characters; pipe the key alone"},
		{strings.Repeat("k", ciAPIKeyMaxBytes+1), "--api-key-stdin: stdin holds more than 4 KiB, which is no API key"},
	} {
		res := runCI(t, unusedLoginFactory(t), tc.stdin, "commits", "--product", ciTestProduct, "--api-key-stdin", "--api-url", "http://127.0.0.1:1")
		if res.err == nil || res.err.Error() != tc.want {
			t.Errorf("stdin %q: error = %v, want %q", tc.stdin, res.err, tc.want)
		}
		if strings.Contains(res.stderr, "zsk_EXAMPLE") {
			t.Errorf("stdin %q: the error echoes the key: %q", tc.stdin, res.stderr)
		}
	}
}

func TestCI_WithoutAPIKeyStdinUsesTheNormalLogin(t *testing.T) {
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	repo.commit("feat: rotate tokens [ZEN-42]")
	t.Chdir(repo.dir)
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST /api/ci/commits": commitsReply})
	f, _ := testFactory(srv)

	res := runCI(t, f, "zsk_EXAMPLE_ignored", "commits", "--product", ciTestProduct)

	if res.err != nil {
		t.Fatalf("commits: %v", res.err)
	}
	if call := api.only(t, http.MethodPost, "/api/ci/commits"); call.APIKey != "zsk_test" {
		t.Fatalf("X-API-Key = %q, want the stored login's key", call.APIKey)
	}
}

func TestCI_EveryCommandNeedsAProductUUID(t *testing.T) {
	clearGitHubEnv(t)
	for _, command := range [][]string{{"commits"}, {"pull-request", "--number", "1", "--title", "t"}, {"coverage", "--file", "testdata/coverage-summary.json"}} {
		res := runCI(t, unusedLoginFactory(t), "", command...)
		if res.err == nil || res.err.Error() != "--product is required" {
			t.Errorf("%v: error = %v", command, res.err)
		}
		res = runCI(t, unusedLoginFactory(t), "", append(command, "--product", "shop\n")...)
		if res.err == nil || res.err.Error() != `--product must be a UUID, got "shop"` {
			t.Errorf("%v: error = %v", command, res.err)
		}
	}
}

func TestCI_ErrorsAreAnnotationsOnlyWithGitHub(t *testing.T) {
	clearGitHubEnv(t)
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/ci/pull-requests": reply(http.StatusNotFound, `{"code":"not_found","message":"product 100% gone\n::error::forged"}`),
	})
	f, _ := testFactory(srv)
	plain := runCI(t, f, "", "pull-request", "--product", ciTestProduct, "--number", "17", "--title", "[ZEN-42] Rotate")
	if plain.stderr != "error: product 100% gone::error::forged (status 404, code not_found)\n" {
		t.Fatalf("stderr without --github = %q", plain.stderr)
	}
	f, _ = testFactory(srv)
	annotated := runCI(t, f, "", "pull-request", "--github", "--json", "--product", ciTestProduct, "--number", "17", "--title", "[ZEN-42] Rotate")
	want := "::error::product 100%25 gone::error::forged (status 404, code not_found)\n" +
		`{"error":{"status":404,"code":"not_found","message":"product 100% gone::error::forged"}}` + "\n"
	if annotated.stderr != want {
		t.Fatalf("stderr with --github = %q\nwant %q", annotated.stderr, want)
	}
}

func TestCI_WorkflowCommandEncodesPercentAndLineBreaks(t *testing.T) {
	if got := workflowCommand("warning", "50% done\r\n::error::x\nnext"); got != "::warning::50%25 done%0D%0A::error::x%0Anext" {
		t.Fatalf("workflowCommand = %q", got)
	}
	var stderr bytes.Buffer
	ran := &cobra.Command{Use: "coverage", Annotations: map[string]string{githubAnnotationsAnnotation: "true"}}
	ran.Flags().Bool("github", false, "")
	_ = ran.Flags().Set("github", "true")
	WriteError(&stderr, ran, nil, &multiLineError{})
	if stderr.String() != "::error::first%0A::error::second\n" {
		t.Fatalf("annotated multi-line error = %q", stderr.String())
	}
	stderr.Reset()
	other := &cobra.Command{Use: "other"}
	other.Flags().Bool("github", true, "")
	WriteError(&stderr, other, nil, errors.New("plain"))
	if stderr.String() != "error: plain\n" {
		t.Fatalf("a command outside zensu ci annotated its error: %q", stderr.String())
	}
}

func TestCI_AnnotatesAFlagErrorWhenTheArgumentsAskForGitHub(t *testing.T) {
	clearGitHubEnv(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"coverage", "--fil", "x", "--github"}, "::error::unknown flag: --fil\n"},
		{[]string{"commits", "--rnage=x", "--github=true"}, "::error::unknown flag: --rnage\n"},
		{[]string{"coverage", "--fil", "x", "--github=false"}, "error: unknown flag: --fil\n"},
		{[]string{"coverage", "--fil", "x"}, "error: unknown flag: --fil\n"},
		{[]string{"coverage", "--fil", "x", "--", "--github"}, "error: unknown flag: --fil\n"},
	} {
		res := runCI(t, unusedLoginFactory(t), "", tc.args...)
		if res.err == nil || res.stderr != tc.want {
			t.Errorf("%v: stderr = %q, want %q (err %v)", tc.args, res.stderr, tc.want, res.err)
		}
	}
}

type multiLineError struct{}

func (*multiLineError) Error() string { return "first\n::error::second" }

func TestCI_ServerTextCannotStartAWorkflowCommand(t *testing.T) {
	clearGitHubEnv(t)
	summary := filepath.Join(t.TempDir(), "summary.md")
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/ci/coverage":      reply(http.StatusOK, `{"mappedFeatures":1,"entries":[{"slug":"auth\n::error::slug","coveragePercent":75}],"unmappedPaths":["a\r\n::warning::path"]}`),
		"POST /api/ci/pull-requests": reply(http.StatusOK, `{"prNumber":17,"linked":[{"ref":"ZEN-42\n::error::ref"}],"unresolved":[],"workOrderMergeError":"merge\n::error::evidence"}`),
	})
	for _, github := range []bool{false, true} {
		args := func(rest ...string) []string {
			if github {
				rest = append(rest, "--github")
			}
			return rest
		}
		f, _ := testFactory(srv)
		coverage := runCI(t, f, "", args("coverage", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json", "--root", "/workspace/shop", "--markdown-summary", summary)...)
		f, _ = testFactory(srv)
		pull := runCI(t, f, "", args("pull-request", "--product", ciTestProduct, "--number", "17", "--title", "[ZEN-42] Rotate")...)
		for _, res := range []ciResult{coverage, pull} {
			if res.err != nil {
				t.Fatalf("github=%v: %v", github, res.err)
			}
			for _, line := range res.lines() {
				if strings.HasPrefix(strings.TrimLeft(line, " \t"), "::") && !(github && strings.HasPrefix(line, "::warning::Zensu kept pull request #17")) {
					t.Errorf("github=%v: a response started a workflow command: %q", github, line)
				}
			}
		}
	}
	data, err := os.ReadFile(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "::") {
			t.Errorf("summary line %q starts a workflow command", line)
		}
	}
}

func TestCI_RejectsAnswersThatAreNotUploadResults(t *testing.T) {
	clearGitHubEnv(t)
	fixture, err := filepath.Abs("testdata/coverage-summary.json")
	if err != nil {
		t.Fatal(err)
	}
	repo := newCIGitRepo(t)
	repo.commit("feat: rotate tokens [ZEN-42]")
	t.Chdir(repo.dir)
	shared := []string{
		"<!doctype html><html><body><h1>Sign in</h1><p>zsk_test</p></body></html>",
		"",
		`{"status":"ok","echo":"zsk_test"}`,
		`["zsk_test"]`,
		`null`,
		`"zsk_test"`,
	}
	cases := []struct {
		args    []string
		path    string
		what    string
		valid   string
		answers []string
	}{
		{[]string{"commits"}, "/api/ci/commits", "upload commits", `{"processed":1,"links":[],"echo":"zsk_test"}`, []string{
			`{"links":[],"echo":"zsk_test"}`,
			`{"processed":"3","links":[],"echo":"zsk_test"}`,
			`{"processed":null,"links":[],"echo":"zsk_test"}`,
			`{"processed":3,"links":null,"echo":"zsk_test"}`,
			`{"processed":1,"links":[],"echo":"zsk_test"}{"processed":1,"links":[]}`,
			`{"processed":1,"links":[{"linked":"one"}],"echo":"zsk_test"}`,
		}},
		{[]string{"pull-request", "--number", "17", "--title", "[ZEN-42] Rotate"}, "/api/ci/pull-requests", "upload pull request #17", `{"prNumber":17,"linked":[],"unresolved":[],"echo":"zsk_test"}`, []string{
			`{"prNumber":"17","linked":[],"unresolved":[],"echo":"zsk_test"}`,
			`{"prNumber":null,"linked":[],"unresolved":[],"echo":"zsk_test"}`,
			`{"prNumber":17,"linked":null,"unresolved":[],"echo":"zsk_test"}`,
			`{"prNumber":17,"linked":[],"echo":"zsk_test"}`,
			`{"prNumber":17,"linked":[{"ref":42}],"unresolved":[],"echo":"zsk_test"}`,
		}},
		{[]string{"coverage", "--file", fixture, "--root", "/workspace/shop"}, "/api/ci/coverage", "upload coverage", `{"mappedFeatures":0,"entries":[],"unmappedPaths":[],"echo":"zsk_test"}`, []string{
			`{"mappedFeatures":"3","entries":[],"unmappedPaths":[],"echo":"zsk_test"}`,
			`{"mappedFeatures":null,"entries":[],"unmappedPaths":[],"echo":"zsk_test"}`,
			`{"mappedFeatures":1,"unmappedPaths":[],"echo":"zsk_test"}`,
			`{"mappedFeatures":0,"entries":[],"unmappedPaths":null,"echo":"zsk_test"}`,
			`{"mappedFeatures":1,"entries":[{"slug":7}],"unmappedPaths":[],"echo":"zsk_test"}`,
		}},
	}
	for _, tc := range cases {
		run := func(answer, summary string) (ciResult, *fakeAPI) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST " + tc.path: reply(http.StatusOK, answer)})
			f, _ := testFactory(srv)
			args := append(append([]string{}, tc.args...), "--product", ciTestProduct, "--github")
			if tc.path == "/api/ci/coverage" {
				args = append(args, "--markdown-summary", summary)
			}
			return runCI(t, f, "", args...), api
		}
		if res, _ := run(tc.valid, filepath.Join(t.TempDir(), "summary.md")); res.err != nil {
			t.Fatalf("%s: the valid answer the cases change was refused: %v", tc.path, res.err)
		}
		for _, answer := range append(append([]string{}, shared...), tc.answers...) {
			shown := strings.TrimSpace(sanitizeTerminal(strings.ReplaceAll(answer, "zsk_test", "***")))
			if shown == "" {
				shown = "empty"
			}
			summary := filepath.Join(t.TempDir(), "summary.md")
			res, api := run(answer, summary)
			want := "the answer to " + tc.what + " is not a Zensu upload result; check that the API URL points at the Zensu API (answer: " + shown + ")"
			if res.err == nil || res.err.Error() != want {
				t.Errorf("%s %q: error = %v\nwant %q", tc.path, answer, res.err, want)
			}
			if strings.Contains(res.stdout+res.stderr, "zsk_test") {
				t.Errorf("%s %q: the key was echoed: %q", tc.path, answer, res.stderr)
			}
			if n := strings.Count(res.stdout+res.stderr, "::error::"); n != 1 || res.stdout != "" {
				t.Errorf("%s %q: stdout %q stderr %q, want one ::error:: line only", tc.path, answer, res.stdout, res.stderr)
			}
			if _, err := os.Stat(summary); !os.IsNotExist(err) {
				t.Errorf("%s %q: a non-result filled the summary", tc.path, answer)
			}
			if api.count(http.MethodPost, tc.path) != 1 {
				t.Errorf("%s %q: %d calls", tc.path, answer, api.count(http.MethodPost, tc.path))
			}
		}
	}
}

func TestCI_GroupIsRegisteredAtTheRoot(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"coverage", "commits", "pull-request"} {
		cmd, _, err := root.Find([]string{"ci", name})
		if err != nil || cmd.Name() != name {
			t.Fatalf("zensu ci %s not found: %v", name, err)
		}
		for _, flag := range []string{"product", "github", "api-key-stdin", "json"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("zensu ci %s has no --%s", name, flag)
			}
		}
		if !IsRequiredFlag(cmd.Flags().Lookup("product")) {
			t.Errorf("zensu ci %s --product is not marked required", name)
		}
	}
}
