package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

type reportFixture struct {
	t          *testing.T
	api        *fakeAPI
	srv        *httptest.Server
	factory    *Factory
	out        *bytes.Buffer
	captureDir string
	clock      *progressTestClock
}

func newReportFixture(t *testing.T, routes map[string]func(recordedRequest) fakeReply) *reportFixture {
	t.Helper()
	aiWorkTestRepo(t)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	srv, api := newFakeAPI(t, routes)
	f, out := loginFactory(srv)
	return &reportFixture{t: t, api: api, srv: srv, factory: f, out: out, captureDir: filepath.Join(dir, ".git", aiWorkCaptureDirName), clock: useProgressClock(t, progressTestTime)}
}

func (f *reportFixture) run(stdin string, args ...string) (map[string]any, error) {
	f.t.Helper()
	f.out.Reset()
	cmd := NewAIWorkCmd(f.factory)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := runCmd(f.t, cmd, args...); err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(f.out.Bytes(), &result); err != nil {
		f.t.Fatalf("output %q is not JSON: %v", f.out.String(), err)
	}
	return result, nil
}

func (f *reportFixture) writeReceipt(session, content string) {
	f.t.Helper()
	if err := os.MkdirAll(f.captureDir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.captureDir, session+".json"), []byte(content), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *reportFixture) queued() bool {
	entries, err := os.ReadDir(filepath.Join(f.captureDir, progressDirName))
	return err == nil && len(entries) > 0
}

func receiptJSON(apiURL, extra string) string {
	return `{"schema":1,"id":"` + progressTestWork + `","scope":{"workScope":"product","taskScope":"feature"},"task":"goal","title":"Goal","criteria":"v1","apiUrl":"` + apiURL + `","startHead":"0123456789012345678901234567890123456789"` + extra + `}`
}

func TestAIWorkReport_PostsOneFactFromFlags(t *testing.T) {
	f := newReportFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t)})
	result, err := f.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "step_progress", "--done", "4", "--total", "7", "--label", "Wire the handler")
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 4 || result["status"] != "sent" || result["workId"] != progressTestWork || result["recorded"] != float64(1) || result["queued"] != float64(0) {
		t.Fatalf("result = %v", result)
	}
	call := f.api.only(t, "POST", "/api/ai-work/capture/units/"+progressTestWork+"/progress")
	want := `{"facts":[{"clientEventId":"86434ecd38a4ae5a96ec9f7a:step_progress:59479200","kind":"step_progress","occurredAt":"2026-11-20T10:00:00.000000Z","sessionRef":"86434ecd38a4ae5a96ec9f7a","seq":59479200,"payload":{"done":4,"total":7,"label":"Wire the handler"}}]}`
	if call.Raw != want || call.Auth != "Bearer browser-login" || call.APIKey != "" {
		t.Errorf("request:\n got %s (auth %q)\nwant %s", call.Raw, call.Auth, want)
	}

	f.clock.Advance(3 * time.Second)
	if _, err := f.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "review_round_ended", "--round", "2", "--findings-open", "1", "--findings-blocking", "1", "--finding", "high:open:Token leaks: in the log", "--finding", "low:fixed:Typo", "--limit-reached"); err != nil {
		t.Fatal(err)
	}
	second := f.api.calls()[1]
	if !strings.Contains(second.Raw, `"clientEventId":"86434ecd38a4ae5a96ec9f7a:review_round_ended:59479203"`) || !strings.Contains(second.Raw, `"payload":{"round":2,"findings":{"open":1,"fixed":0,"blocking":1},"items":[{"severity":"high","state":"open","title":"Token leaks: in the log"},{"severity":"low","state":"fixed","title":"Typo"}],"limitReached":true}`) {
		t.Errorf("second request = %s", second.Raw)
	}
	for _, call := range f.api.calls() {
		if strings.Contains(call.Raw+call.Path+call.Query.Encode()+call.Auth, "scv1_") {
			t.Errorf("the capture key left the process in %s %s", call.Method, call.Path)
		}
	}
}

func TestAIWorkReport_RefusesEveryCredentialButThePersonalLogin(t *testing.T) {
	for _, cfg := range []*config.Config{{APIKey: "zsk_shared"}, {AccessToken: "zst_worker"}, {}} {
		f := newReportFixture(t, nil)
		f.factory.NewClient = func(context.Context) (*client.Client, error) { return client.New(cfg, f.srv.URL, ""), nil }
		if _, err := f.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_started"); err == nil || !strings.Contains(err.Error(), "personal OAuth login") {
			t.Errorf("credential %+v must be refused, got %v", cfg, err)
		}
		if len(f.api.calls()) != 0 || f.queued() {
			t.Error("a refused credential sent a request or queued a fact")
		}
	}
}

func TestAIWorkReport_RefusesInvalidInputBeforeAnythingIsQueued(t *testing.T) {
	f := newReportFixture(t, nil)
	base := []string{"report", progressTestWork, "--session", progressTestKey}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"no session":                {[]string{"report", progressTestWork, "--kind", "turn_started"}, "--session must be the exact plugin-provided capture key"},
		"a session that is no key":  {[]string{"report", progressTestWork, "--session", "my-session", "--kind", "turn_started"}, "--session must be the exact plugin-provided capture key"},
		"no kind":                   {base, "--kind is required unless --batch"},
		"an unknown kind":           {append(slices.Clone(base), "--kind", "heartbeat"), `unknown --kind "heartbeat"`},
		"a flag of another kind":    {append(slices.Clone(base), "--kind", "signal", "--done", "1"), "--done does not belong to kind signal"},
		"a label with a line break": {append(slices.Clone(base), "--kind", "step_progress", "--done", "1", "--total", "2", "--label", "one\ntwo"), "label must be a single line"},
		"a label above 120":         {append(slices.Clone(base), "--kind", "step_progress", "--done", "1", "--total", "2", "--label", strings.Repeat("x", 121)), "label must be a single line"},
		"a worktree path":           {append(slices.Clone(base), "--kind", "session_started", "--harness", "codex", "--worktree", "/Users/someone/work"), "worktree must be a label"},
		"a url with a query":        {append(slices.Clone(base), "--kind", "change_request_linked", "--repository", "a/b", "--number", "1", "--url", "https://example.test/pull/1?x=1"), "url must be an https address"},
		"closed by a person":        {append(slices.Clone(base), "--kind", "work_ended", "--reason", "closed_by_person"), "closed_by_person"},
		"a number that is none":     {append(slices.Clone(base), "--kind", "step_progress", "--done", "one", "--total", "2"), "invalid argument"},
		"a timeout of zero":         {append(slices.Clone(base), "--kind", "turn_started", "--timeout", "0s"), "--timeout must be between 1s and 60s"},
		"a timeout above a minute":  {append(slices.Clone(base), "--kind", "turn_started", "--timeout", "2m"), "--timeout must be between 1s and 60s"},
		"batch with a kind":         {append(slices.Clone(base), "--batch", "--kind", "turn_started"), "--batch reads its facts from stdin; drop --kind"},
		"batch with a payload flag": {append(slices.Clone(base), "--batch", "--activity", "editing"), "--batch reads its facts from stdin; drop --activity"},
		"batch without input":       {append(slices.Clone(base), "--batch"), "--batch needs at least one JSON line on stdin"},
		"two work ids":              {[]string{"report", progressTestWork, progressTestWork, "--session", progressTestKey, "--kind", "turn_started"}, "accepts at most 1 arg"},
		"a work id that is none":    {[]string{"report", "../other", "--session", progressTestKey, "--kind", "turn_started"}, "expected a canonical non-nil work/event UUID"},
		"an unknown flag":           {append(slices.Clone(base), "--kind", "turn_started", "--prompt", "what the user asked"), "unknown flag: --prompt"},
	} {
		if _, err := f.run("", tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", name, err, tc.want)
		}
	}
	if len(f.api.calls()) != 0 || f.queued() {
		t.Errorf("invalid input sent %d requests or queued a fact", len(f.api.calls()))
	}
	report, _, err := NewAIWorkCmd(f.factory).Find([]string{"report"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"prompt", "answer", "question", "command", "code", "diff", "path", "file", "transcript", "hostname"} {
		if report.Flags().Lookup(forbidden) != nil {
			t.Errorf("report must have no --%s flag", forbidden)
		}
	}
}

func TestAIWorkReport_SendsABatchFromStdinInItsOrder(t *testing.T) {
	f := newReportFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t)})
	stdin := `{"kind":"turn_ended"}` + "\n" +
		`{"kind":"workspace_state","payload":{"commitsAhead":2,"filesChanged":9,"uncommittedFiles":0,"head":"bc15472abc"}}` + "\n" +
		`{"kind":"evidence_reported","payload":{"check":"tests","outcome":"passed","passed":412,"failed":0}}` + "\n"
	result, err := f.run(stdin, "report", progressTestWork, "--session", progressTestKey, "--batch")
	if err != nil {
		t.Fatal(err)
	}
	if result["status"] != "sent" || result["recorded"] != float64(3) {
		t.Fatalf("result = %v", result)
	}
	call := f.api.only(t, "POST", "/api/ai-work/capture/units/"+progressTestWork+"/progress")
	if got := factKinds(t, call.Raw); !slices.Equal(got, []string{"turn_ended", "workspace_state", "evidence_reported"}) {
		t.Errorf("facts = %v", got)
	}
	if !strings.Contains(call.Raw, `"seq":59479200`) || !strings.Contains(call.Raw, `"seq":59479201`) || !strings.Contains(call.Raw, `"seq":59479202,"payload":{"check":"tests","outcome":"passed","passed":412,"failed":0}`) {
		t.Errorf("the queue numbers the lines in their order: %s", call.Raw)
	}

	before := len(f.api.calls())
	for name, bad := range map[string]string{
		"an unknown key":           `{"kind":"turn_started"}` + "\n" + `{"kind":"turn_ended","seq":7}`,
		"an unknown payload field": `{"kind":"blocked","payload":{"kind":"other","command":"rm -rf"}}`,
		"an invalid line":          `{"kind":"turn_started"}` + "\n" + `not json`,
		"101 lines":                strings.Repeat(`{"kind":"turn_started"}`+"\n", 101),
		"more than 256 KiB":        `{"kind":"turn_started"}` + "\n" + strings.Repeat(" ", progressMaxBatchBytes),
	} {
		if _, err := f.run(bad, "report", progressTestWork, "--session", progressTestKey, "--batch"); err == nil {
			t.Errorf("%s must refuse the whole call", name)
		}
	}
	if len(f.api.calls()) != before {
		t.Errorf("a refused batch sent %d requests", len(f.api.calls())-before)
	}
	state := &progressState{}
	raw, err := os.ReadFile(filepath.Join(f.captureDir, progressDirName, progressTestWork+".json"))
	if err != nil || json.Unmarshal(raw, state) != nil || state.pending() != 0 {
		t.Errorf("a refused batch must queue nothing: %v, %+v", err, state)
	}
}

func TestAIWorkReport_TakesTheWorkFromTheCaptureReceipt(t *testing.T) {
	f := newReportFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t)})
	args := []string{"report", "--session", progressTestKey, "--kind", "turn_started"}

	result, err := f.run("", args...)
	if err != nil || len(result) != 4 || result["status"] != "skipped" || result["reason"] != "no_active_work" {
		t.Fatalf("without a receipt nothing is reported, got %v, %v", result, err)
	}

	for reason, content := range map[string]string{
		"invalid_receipt": `{"schema":2,"id":"` + progressTestWork + `"}`,
		"legacy_scope":    `{"schema":1,"id":"` + progressTestWork + `","apiUrl":"` + f.srv.URL + `"}`,
		"capture_paused":  receiptJSON(f.srv.URL, `,"capturePaused":true`),
		"host_mismatch":   receiptJSON("https://other.example.test", ""),
	} {
		f.writeReceipt(progressTestKey, content)
		result, err := f.run("", args...)
		if err != nil || result["status"] != "skipped" || result["reason"] != reason {
			t.Errorf("%s: got %v, %v", reason, result, err)
		}
	}
	f.writeReceipt(progressTestKey, `not json`)
	if result, err := f.run("", args...); err != nil || result["reason"] != "invalid_receipt" {
		t.Errorf("a receipt that is no JSON: got %v, %v", result, err)
	}
	f.writeReceipt(progressTestKey, `{"schema":1,"id":"`+progressTestWork+`","scope":{"workScope":"product"},"apiUrl":"`+f.srv.URL+`"}`)
	if result, err := f.run("", args...); err != nil || result["reason"] != "legacy_scope" {
		t.Errorf("a receipt without a goal scope: got %v, %v", result, err)
	}
	if len(f.api.calls()) != 0 || f.queued() {
		t.Fatalf("a skipped report sent %d requests or queued a fact", len(f.api.calls()))
	}

	f.writeReceipt(progressTestKey, receiptJSON(f.srv.URL, `,"capturePaused":true`))
	if result, err := f.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_started"); err != nil || result["status"] != "sent" {
		t.Errorf("a work id on the command line reads no receipt, got %v, %v", result, err)
	}

	f.writeReceipt(progressTestKey, receiptJSON(f.srv.URL, `,"pending":{"id":"891419fa-f7e4-4a52-8123-2bef791876dd"}`))
	result, err = f.run("", args...)
	if err != nil || result["status"] != "sent" || result["workId"] != progressTestWork {
		t.Fatalf("a receipt with a confirmed goal names the unit, got %v, %v", result, err)
	}
	if got := f.api.count("POST", "/api/ai-work/capture/units/"+progressTestWork+"/progress"); got != 2 {
		t.Errorf("progress requests = %d, want 2", got)
	}

	if runtime.GOOS != "windows" {
		other := strings.Replace(progressTestKey, "scv1_a", "scv1_b", 1)
		if err := os.Symlink(filepath.Join(f.captureDir, progressTestKey+".json"), filepath.Join(f.captureDir, other+".json")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.run("", "report", "--session", other, "--kind", "turn_started"); err == nil || !strings.Contains(err.Error(), "unsafe capture receipt") {
			t.Errorf("a receipt that is a symbolic link must be refused, got %v", err)
		}
	}
}

func TestAIWorkReport_NeedsAGitWorktree(t *testing.T) {
	t.Chdir(t.TempDir())
	srv, api := newFakeAPI(t, nil)
	f, _ := loginFactory(srv)
	err := runCmd(t, NewAIWorkCmd(f), "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_started")
	if err == nil || !strings.Contains(err.Error(), "a Git worktree is required") || len(api.calls()) != 0 {
		t.Errorf("outside a Git worktree the report must fail before any request, got %v", err)
	}
}

func TestAIWorkReport_AnswersUnsupportedWithExitZero(t *testing.T) {
	f := newReportFixture(t, map[string]func(recordedRequest) fakeReply{
		progressRoute: func(recordedRequest) fakeReply { return fakeReply{Status: 404, Body: "404 page not found\n"} },
		detailRoute:   readableDetail(),
	})
	result, err := f.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_started")
	if err != nil {
		t.Fatalf("a server without the route must not fail the report: %v", err)
	}
	if len(result) != 5 || result["status"] != "unsupported" || result["reason"] != "route_missing" || result["recorded"] != float64(0) || result["queued"] != float64(0) {
		t.Errorf("result = %v", result)
	}
	again, err := f.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_ended")
	if err != nil || again["status"] != "unsupported" || len(f.api.calls()) != 2 {
		t.Errorf("the verdict is remembered across calls, got %v, %v after %d requests", again, err, len(f.api.calls()))
	}

	refused := newReportFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply {
		return refusal(403, "forbidden", "only the current owner may report progress on this work", nil)
	}})
	if _, err := refused.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_started"); err == nil || !strings.Contains(err.Error(), "(status 403, code forbidden)") {
		t.Errorf("a refusal of the whole work is an error, got %v", err)
	}

	queued := newReportFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: reply(503, "down")})
	held, err := queued.run("", "report", progressTestWork, "--session", progressTestKey, "--kind", "turn_started", "--timeout", "2s")
	if err != nil || held["status"] != "queued" || held["reason"] != "server_error" || held["queued"] != float64(1) {
		t.Errorf("a failed send is no error, got %v, %v", held, err)
	}
}

func TestAIWorkReport_HelpNamesEveryKindAndTheKeyRule(t *testing.T) {
	srv, _ := newFakeAPI(t, nil)
	f, _ := loginFactory(srv)
	report, _, err := NewAIWorkCmd(f).Find([]string{"report"})
	if err != nil {
		t.Fatal(err)
	}
	usage := report.Flags().Lookup("kind").Usage
	for _, kind := range progressFactKinds {
		if !strings.Contains(usage, kind) {
			t.Errorf("--kind help does not name %s", kind)
		}
	}
	if !IsRequiredFlag(report.Flags().Lookup("session")) || IsRequiredFlag(report.Flags().Lookup("kind")) {
		t.Error("--session is always required, --kind only without --batch")
	}
	for _, name := range progressPayloadFlagNames() {
		if report.Flags().Lookup(name) == nil {
			t.Errorf("payload flag --%s is not registered", name)
		}
	}
	registered := 0
	report.Flags().VisitAll(func(*pflag.Flag) { registered++ })
	if want := len(progressPayloadFlagNames()) + 5; registered != want {
		t.Errorf("report has %d flags, want the %d payload flags plus --session, --kind, --batch, --timeout and --json", registered, len(progressPayloadFlagNames()))
	}
	if !strings.Contains(report.Long, "never sent") || !strings.Contains(report.Long, "exit code 0") {
		t.Error("the long help states what never leaves and that the typed results exit 0")
	}
}
