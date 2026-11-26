package ciaction

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	actionKey     = "zsk_EXAMPLE_action_key"
	actionProduct = "7c1e7d4a-2b9f-4c51-9d3e-8a6f5b4c3d21"
	actionRunURL  = "https://github.com/acme/shop/actions/runs/4242"
)

var (
	cliOnce     sync.Once
	archiveOnce sync.Once
	cliDir      string
	cliBinary   []byte
	cliArchive  []byte
	cliErr      error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if cliDir != "" {
		_ = os.RemoveAll(cliDir)
	}
	os.Exit(code)
}

func builtCLIBinary(t *testing.T) []byte {
	t.Helper()
	cliOnce.Do(func() {
		if cliDir, cliErr = os.MkdirTemp("", "zensu-ciaction-"); cliErr != nil {
			return
		}
		bin := filepath.Join(cliDir, "zensu")
		build := exec.Command("go", "build", "-o", bin, "./cmd/zensu")
		build.Dir = repoRoot(t)
		if out, err := build.CombinedOutput(); err != nil {
			cliErr = fmt.Errorf("go build ./cmd/zensu: %v\n%s", err, out)
			return
		}
		cliBinary, cliErr = os.ReadFile(bin)
	})
	if cliErr != nil {
		t.Fatal(cliErr)
	}
	return cliBinary
}

func builtCLIArchive(t *testing.T) []byte {
	t.Helper()
	binary := builtCLIBinary(t)
	archiveOnce.Do(func() { cliArchive = tarball(t, binary) })
	return cliArchive
}

type actionInput struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
}

type actionStep struct {
	Name             string            `yaml:"name"`
	ID               string            `yaml:"id"`
	If               string            `yaml:"if"`
	Shell            string            `yaml:"shell"`
	WorkingDirectory string            `yaml:"working-directory"`
	Env              map[string]string `yaml:"env"`
	Run              string            `yaml:"run"`
	Uses             string            `yaml:"uses"`
	With             map[string]string `yaml:"with"`
}

type compositeAction struct {
	Name        string                 `yaml:"name"`
	Description string                 `yaml:"description"`
	Inputs      map[string]actionInput `yaml:"inputs"`
	Runs        struct {
		Using string       `yaml:"using"`
		Steps []actionStep `yaml:"steps"`
	} `yaml:"runs"`
}

func loadYAML(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func loadAction(t *testing.T) compositeAction {
	t.Helper()
	var action compositeAction
	loadYAML(t, filepath.Join(repoRoot(t), "actions", "coverage", "action.yml"), &action)
	return action
}

type zensuCall struct {
	Method      string
	Path        string
	Key         string
	ContentType string
	Body        string
}

type fakeZensu struct {
	mu     sync.Mutex
	calls  []zensuCall
	server *httptest.Server
	reject map[string]string
}

var referencePattern = regexp.MustCompile(`\[([A-Za-z][A-Za-z0-9]*)-([0-9A-Za-z_-]+)\]`)

func resolves(tail string) bool {
	return tail == "42" || tail == "7" || tail == "invoice-rounding"
}

func newFakeZensu(t *testing.T) *fakeZensu {
	t.Helper()
	z := &fakeZensu{reject: map[string]string{}}
	z.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		z.mu.Lock()
		z.calls = append(z.calls, zensuCall{r.Method, r.URL.Path, r.Header.Get("X-API-Key"), r.Header.Get("Content-Type"), string(body)})
		rejection, rejected := z.reject[r.URL.Path]
		z.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if rejected {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, rejection)
			return
		}
		var answer any
		switch r.URL.Path {
		case "/api/ci/commits":
			answer = commitsAnswer(body)
		case "/api/ci/pull-requests":
			answer = pullRequestAnswer(body)
		case "/api/ci/coverage":
			answer = json.RawMessage(`{"mappedFeatures":3,"entries":[` +
				`{"featureId":"f1","slug":"auth-login","coveragePercent":75,"linesCovered":90,"linesTotal":120,"branchCoverage":60},` +
				`{"featureId":"f2","slug":"auth-tokens","coveragePercent":75,"linesCovered":90,"linesTotal":120,"branchCoverage":60},` +
				`{"featureId":"f3","slug":"invoices","coveragePercent":90,"linesCovered":45,"linesTotal":50,"branchCoverage":90}],` +
				`"unmappedPaths":["/opt/other/outside.ts","scripts/build.js"]}`)
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(answer)
	}))
	t.Cleanup(z.server.Close)
	return z
}

func commitsAnswer(body []byte) map[string]any {
	var req struct {
		Commits []struct {
			Hash    string `json:"hash"`
			Message string `json:"message"`
		} `json:"commits"`
	}
	_ = json.Unmarshal(body, &req)
	links := []map[string]any{}
	for _, c := range req.Commits {
		refs, unresolved, linked := []string{}, []string{}, 0
		for _, m := range referencePattern.FindAllStringSubmatch(c.Message, -1) {
			refs = append(refs, strings.ToUpper(m[1])+"-"+m[2])
			if resolves(m[2]) {
				linked++
			} else {
				unresolved = append(unresolved, m[2])
			}
		}
		links = append(links, map[string]any{"commitHash": c.Hash, "featureSlugs": []string{}, "featureRefs": refs, "linked": linked, "unresolved": unresolved})
	}
	return map[string]any{"processed": len(req.Commits), "links": links}
}

func pullRequestAnswer(body []byte) map[string]any {
	var req struct {
		PRNumber int    `json:"prNumber"`
		Title    string `json:"title"`
		Body     string `json:"body"`
	}
	_ = json.Unmarshal(body, &req)
	linked, unresolved := []map[string]any{}, []string{}
	for _, m := range referencePattern.FindAllStringSubmatch(req.Title+" "+req.Body, -1) {
		if resolves(m[2]) {
			linked = append(linked, map[string]any{"slug": m[2], "ref": strings.ToUpper(m[1]) + "-" + m[2], "featureId": "f-" + m[2]})
		} else {
			unresolved = append(unresolved, m[2])
		}
	}
	return map[string]any{"prNumber": req.PRNumber, "linked": linked, "unresolved": unresolved, "workOrderMergeRecorded": false}
}

func (z *fakeZensu) recorded() []zensuCall {
	z.mu.Lock()
	defer z.mu.Unlock()
	return append([]zensuCall(nil), z.calls...)
}

type history struct {
	dir                              string
	base, first, typo, bump, billing string
}

func gitIsolation() []string {
	return []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1"}
}

func newHistory(t *testing.T) history {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is needed: %v", err)
	}
	h := history{dir: t.TempDir()}
	git := func(env []string, args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = h.dir
		cmd.Env = append(append(os.Environ(), gitIsolation()...), env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(nil, "init", "-q", "-b", "main")
	git(nil, "config", "user.name", "Ada Lovelace")
	git(nil, "config", "user.email", "ada@example.test")
	commit := func(message, date string) string {
		git([]string{"GIT_AUTHOR_DATE=" + date, "GIT_COMMITTER_DATE=" + date}, "commit", "-q", "--allow-empty", "-m", message)
		return git(nil, "rev-parse", "HEAD")
	}
	h.base = commit("chore: initial import", "2026-10-01T09:00:00+02:00")
	h.first = commit("feat(auth): rotate refresh tokens [ZEN-42]", "2026-10-02T10:15:00+02:00")
	h.typo = commit("docs: fix a typo", "2026-10-03T11:00:00+02:00")
	h.bump = commit("chore: bump the token library\n\nRefs [ZEN-42]", "2026-10-04T08:45:00+02:00")
	h.billing = commit("fix(billing): round invoice totals [zen-invoice-rounding]\n\nAlso closes [ZEN-999].", "2026-10-05T12:30:00+02:00")
	return h
}

func writeCoverageReport(t *testing.T, workspace string) {
	t.Helper()
	entry := func(lc, lt, bc, bt int) map[string]any {
		return map[string]any{
			"lines":    map[string]any{"total": lt, "covered": lc, "skipped": 0, "pct": 0},
			"branches": map[string]any{"total": bt, "covered": bc, "skipped": 0, "pct": 0},
		}
	}
	report := map[string]any{
		"total":                 entry(140, 210, 15, 20),
		"/opt/other/outside.ts": entry(5, 10, 0, 0),
		filepath.Join(workspace, "scripts", "build.js"):          entry(0, 30, 0, 0),
		filepath.Join(workspace, "src", "auth", "login.ts"):      entry(80, 100, 6, 10),
		filepath.Join(workspace, "src", "auth", "token.ts"):      entry(10, 20, 0, 0),
		filepath.Join(workspace, "src", "billing", "invoice.ts"): entry(45, 50, 9, 10),
		filepath.Join(workspace, "src", "billing", "empty.d.ts"): entry(0, 0, 0, 0),
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, "coverage"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "coverage", "coverage-summary.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

type actionRun struct {
	t         *testing.T
	action    compositeAction
	inputs    map[string]string
	eventName string
	env       map[string]string
	bash      string
	path      string
	workspace string
	checkout  string
	home      string
	tmp       string
	runner    string
	summary   string
	outputs   map[string]map[string]string
	outcomes  map[string]string
	logs      map[string]string
	ran       []string
	failed    string
}

type runSetup struct {
	inputs    map[string]string
	eventName string
	event     map[string]any
	sha       string
	windows   bool
}

func requireWindowsActionRunner(t *testing.T) (bash, path string) {
	t.Helper()
	powerShells(t)
	if runtime.GOOS != "windows" {
		requireUnixRunner(t)
		stub := t.TempDir()
		if err := os.WriteFile(filepath.Join(stub, "cygpath"), []byte("#!/bin/sh\nprintf '%s\\n' \"$2\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return "bash", stub + string(os.PathListSeparator) + os.Getenv("PATH")
	}
	bash = filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe")
	if _, err := os.Stat(bash); err != nil {
		t.Fatalf("the Windows branch of the action runs in Git Bash: %v", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatalf("git is needed: %v", err)
	}
	return bash, os.Getenv("PATH")
}

func newActionRun(t *testing.T, workspace string, release *fakeRelease, key signingKey, setup runSetup) *actionRun {
	t.Helper()
	bash, path := "bash", os.Getenv("PATH")
	if setup.windows {
		bash, path = requireWindowsActionRunner(t)
	} else {
		requireUnixRunner(t)
	}
	r := &actionRun{
		t:         t,
		action:    loadAction(t),
		inputs:    setup.inputs,
		eventName: setup.eventName,
		bash:      bash,
		path:      path,
		workspace: workspace,
		checkout:  t.TempDir(),
		home:      t.TempDir(),
		tmp:       t.TempDir(),
		runner:    t.TempDir(),
		outputs:   map[string]map[string]string{},
		outcomes:  map[string]string{},
		logs:      map[string]string{},
	}
	r.summary = filepath.Join(r.runner, "step_summary.md")
	if err := os.WriteFile(r.summary, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	actionDir := filepath.Join(r.checkout, "actions", "coverage")
	if err := os.MkdirAll(actionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "actions", "coverage", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(actionDir, "action.yml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	patchedInstallScript(t, r.checkout, key.public)
	patchedInstallPS1(t, r.checkout, key.public)
	eventPath := filepath.Join(r.runner, "event.json")
	eventData, err := json.Marshal(setup.event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(eventPath, eventData, 0o644); err != nil {
		t.Fatal(err)
	}
	runnerOS := map[string]string{"darwin": "macOS", "linux": "Linux"}[runtime.GOOS]
	if setup.windows {
		runnerOS = "Windows"
	}
	r.env = map[string]string{
		"CI":                  "true",
		"GITHUB_ACTIONS":      "true",
		"GITHUB_ACTION_PATH":  actionDir,
		"GITHUB_WORKSPACE":    workspace,
		"GITHUB_STEP_SUMMARY": r.summary,
		"GITHUB_EVENT_NAME":   setup.eventName,
		"GITHUB_EVENT_PATH":   eventPath,
		"GITHUB_SHA":          setup.sha,
		"GITHUB_SERVER_URL":   "https://github.com",
		"GITHUB_REPOSITORY":   "acme/shop",
		"GITHUB_RUN_ID":       "4242",
		"RUNNER_TEMP":         r.runner,
		"RUNNER_OS":           runnerOS,
		"HOME":                r.home,
		"TMPDIR":              r.tmp,
		"GIT_CONFIG_GLOBAL":   os.DevNull,
		"GIT_CONFIG_NOSYSTEM": "1",
	}
	if release != nil {
		if _, ok := r.inputs["release-url"]; !ok {
			r.inputs["release-url"] = release.server.URL
		}
	}
	return r
}

var (
	expressionPattern  = regexp.MustCompile(`\$\{\{\s*(.*?)\s*\}\}`)
	conditionPattern   = regexp.MustCompile(`^([A-Za-z0-9_.-]+) (==|!=) '([^']*)'$`)
	statusCheckPattern = regexp.MustCompile(`\b(always|cancelled|failure|success)\(\)`)
)

func (r *actionRun) value(ref string) string {
	r.t.Helper()
	switch {
	case strings.HasPrefix(ref, "inputs."):
		name := strings.TrimPrefix(ref, "inputs.")
		input, ok := r.action.Inputs[name]
		if !ok {
			r.t.Fatalf("the action reads the undeclared input %q", name)
		}
		if v, ok := r.inputs[name]; ok {
			return v
		}
		return input.Default
	case strings.HasPrefix(ref, "steps."):
		parts := strings.Split(ref, ".")
		switch {
		case len(parts) == 4 && parts[2] == "outputs":
			return r.outputs[parts[1]][parts[3]]
		case len(parts) == 3 && parts[2] == "outcome":
			return r.outcomes[parts[1]]
		}
		r.t.Fatalf("unsupported expression %q", ref)
	case ref == "github.event_name":
		return r.eventName
	}
	r.t.Fatalf("unsupported expression %q", ref)
	return ""
}

func (r *actionRun) interpolate(s string) string {
	return expressionPattern.ReplaceAllStringFunc(s, func(m string) string {
		return r.value(expressionPattern.FindStringSubmatch(m)[1])
	})
}

func (r *actionRun) condition(expr string) bool {
	r.t.Helper()
	expr = strings.TrimSpace(expr)
	if inner, ok := strings.CutPrefix(expr, "${{"); ok {
		expr = strings.TrimSpace(strings.TrimSuffix(inner, "}}"))
	}
	if expr == "" {
		return true
	}
	for _, clause := range strings.Split(expr, "&&") {
		clause = strings.TrimSpace(clause)
		if clause == "!cancelled()" {
			continue
		}
		m := conditionPattern.FindStringSubmatch(clause)
		if m == nil {
			r.t.Fatalf("unsupported if: %q", expr)
		}
		if strings.EqualFold(r.value(m[1]), m[3]) != (m[2] == "==") {
			return false
		}
	}
	return true
}

func (r *actionRun) run() *actionRun {
	r.t.Helper()
	for _, step := range r.action.Runs.Steps {
		if (r.failed != "" && !statusCheckPattern.MatchString(step.If)) || !r.condition(step.If) {
			r.outcomes[step.ID] = "skipped"
			continue
		}
		r.ran = append(r.ran, step.ID)
		if r.runStep(step) {
			r.outcomes[step.ID] = "success"
			continue
		}
		r.outcomes[step.ID] = "failure"
		if r.failed == "" {
			r.failed = step.ID
		}
	}
	return r
}

func (r *actionRun) runStep(step actionStep) bool {
	r.t.Helper()
	if step.Shell != "bash" || step.Run == "" {
		r.t.Fatalf("step %q is not a bash run step", step.ID)
	}
	script := filepath.Join(r.runner, step.ID+".sh")
	if err := os.WriteFile(script, []byte(step.Run), 0o644); err != nil {
		r.t.Fatal(err)
	}
	output := filepath.Join(r.runner, step.ID+".output")
	if err := os.WriteFile(output, nil, 0o644); err != nil {
		r.t.Fatal(err)
	}
	var env []string
	if runtime.GOOS == "windows" {
		for _, kv := range os.Environ() {
			if name, _, _ := strings.Cut(kv, "="); inheritedOnWindows(name) && !strings.EqualFold(name, "PATH") {
				env = append(env, kv)
			}
		}
		env = append(env, "TEMP="+r.tmp, "TMP="+r.tmp)
	}
	env = append(env, "PATH="+r.path, "LANG=C", "LC_ALL=C", "GITHUB_OUTPUT="+output)
	for k, v := range r.env {
		env = append(env, k+"="+v)
	}
	for k, v := range step.Env {
		env = append(env, k+"="+r.interpolate(v))
	}
	cmd := exec.Command(r.bash, "--noprofile", "--norc", "-eo", "pipefail", script)
	cmd.Dir = r.workspace
	if dir := r.interpolate(step.WorkingDirectory); dir != "" {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(r.workspace, dir)
		}
		cmd.Dir = dir
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	r.logs[step.ID] = string(out)
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		r.t.Fatalf("step %q: %v", step.ID, err)
	}
	r.outputs[step.ID] = readStepOutputs(r.t, output)
	return err == nil
}

func readStepOutputs(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	outputs := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			t.Fatalf("unsupported GITHUB_OUTPUT line %q", scanner.Text())
		}
		outputs[name] = value
	}
	return outputs
}

func (r *actionRun) requireSteps(want ...string) {
	r.t.Helper()
	if !slices.Equal(r.ran, want) || r.failed != "" {
		r.t.Fatalf("ran %v (failed %q), want %v\n%s", r.ran, r.failed, want, r.allLogs())
	}
}

func (r *actionRun) allLogs() string {
	var b strings.Builder
	for _, id := range r.ran {
		fmt.Fprintf(&b, "--- %s\n%s", id, r.logs[id])
	}
	return b.String()
}

func (r *actionRun) requireKeyNowhere() {
	r.t.Helper()
	if strings.Contains(r.allLogs(), actionKey) {
		r.t.Fatalf("the key reached the step output:\n%s", r.allLogs())
	}
	for _, dir := range []string{r.workspace, r.checkout, r.home, r.tmp, r.runner} {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), actionKey) {
				r.t.Errorf("the key was written to %s", path)
			}
			return nil
		})
		if err != nil {
			r.t.Fatal(err)
		}
	}
}

func pullRequestEvent(h history) map[string]any {
	return map[string]any{"pull_request": map[string]any{
		"number":           17,
		"title":            "[ZEN-42] Rotate refresh tokens",
		"body":             "Closes [ZEN-7] and [ZEN-404].",
		"html_url":         "https://github.com/acme/shop/pull/17",
		"state":            "open",
		"merged":           false,
		"merge_commit_sha": "9f1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c",
		"head":             map[string]any{"sha": h.bump, "ref": "feat/rotate-tokens"},
		"base":             map[string]any{"sha": h.base, "ref": "main"},
	}}
}

func configuredInputs(z *fakeZensu, extra map[string]string) map[string]string {
	inputs := map[string]string{
		"api-key":    actionKey,
		"api-url":    z.server.URL + "/",
		"product-id": actionProduct,
	}
	for k, v := range extra {
		inputs[k] = v
	}
	return inputs
}

func jsonEqual(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("got is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want is not JSON: %v\n%s", err, want)
	}
	gotText, _ := json.Marshal(g)
	wantText, _ := json.Marshal(w)
	if string(gotText) != string(wantText) {
		t.Fatalf("JSON differs\n got: %s\nwant: %s", gotText, wantText)
	}
}

func signedCLIRelease(t *testing.T) (*fakeRelease, signingKey) {
	t.Helper()
	requireOpenSSL3(t)
	key := newSigningKey(t)
	return newFakeReleaseOf(t, loadAction(t).Inputs["version"].Default, builtCLIArchive(t), key), key
}

func TestCoverageAction_RunsEveryStepOnAPullRequest(t *testing.T) {
	release, key := signedCLIRelease(t)
	z := newFakeZensu(t)
	h := newHistory(t)
	writeCoverageReport(t, h.dir)

	run := newActionRun(t, h.dir, release, key, runSetup{
		inputs:    configuredInputs(z, map[string]string{"coverage-file": "coverage/coverage-summary.json"}),
		eventName: "pull_request",
		event:     pullRequestEvent(h),
		sha:       "8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d",
	}).run()

	run.requireSteps("config", "install", "commits", "pull-request", "coverage")
	calls := z.recorded()
	if len(calls) != 3 {
		t.Fatalf("%d API calls, want 3: %+v", len(calls), calls)
	}
	for i, path := range []string{"/api/ci/commits", "/api/ci/pull-requests", "/api/ci/coverage"} {
		if calls[i].Method != http.MethodPost || calls[i].Path != path || calls[i].Key != actionKey || calls[i].ContentType != "application/json" {
			t.Fatalf("call %d = %s %s key %q type %q", i, calls[i].Method, calls[i].Path, calls[i].Key, calls[i].ContentType)
		}
	}
	jsonEqual(t, calls[0].Body, fmt.Sprintf(`{"productId":%q,"commits":[
		{"hash":%q,"message":"chore: bump the token library\n\nRefs [ZEN-42]","author":"Ada Lovelace","timestamp":"2026-10-04T08:45:00+02:00"},
		{"hash":%q,"message":"feat(auth): rotate refresh tokens [ZEN-42]","author":"Ada Lovelace","timestamp":"2026-10-02T10:15:00+02:00"}]}`,
		actionProduct, h.bump, h.first))
	jsonEqual(t, calls[1].Body, fmt.Sprintf(`{"productId":%q,"prNumber":17,"title":"[ZEN-42] Rotate refresh tokens","body":"Closes [ZEN-7] and [ZEN-404].",
		"prUrl":"https://github.com/acme/shop/pull/17","status":"open","repository":"https://github.com/acme/shop","baseRef":"main","headSha":%q}`,
		actionProduct, h.bump))
	jsonEqual(t, calls[2].Body, fmt.Sprintf(`{"productId":%q,"source":"ci","ciRunUrl":%q,"files":[
		{"path":"/opt/other/outside.ts","linesCovered":5,"linesTotal":10},
		{"path":"scripts/build.js","linesCovered":0,"linesTotal":30},
		{"path":"src/auth/login.ts","linesCovered":80,"linesTotal":100,"branchesCovered":6,"branchesTotal":10},
		{"path":"src/auth/token.ts","linesCovered":10,"linesTotal":20},
		{"path":"src/billing/empty.d.ts","linesCovered":0,"linesTotal":0},
		{"path":"src/billing/invoice.ts","linesCovered":45,"linesTotal":50,"branchesCovered":9,"branchesTotal":10}]}`,
		actionProduct, actionRunURL))

	for id, want := range map[string]string{
		"install":      "Verified the release signature.\n",
		"commits":      "Zensu linked 2 feature reference(s) from 2 commit(s).\n",
		"pull-request": "Zensu linked pull request #17 to ZEN-42, ZEN-7.\nNo feature of this product matches ZEN-404.\n",
		"coverage":     "Zensu recorded coverage for 3 feature(s); 2 of 6 file(s) matched no component.\n",
	} {
		if !strings.Contains(run.logs[id], want) {
			t.Errorf("step %s output %q lacks %q", id, run.logs[id], want)
		}
	}
	summary, err := os.ReadFile(run.summary)
	if err != nil {
		t.Fatal(err)
	}
	wantSummary := "## Zensu coverage\n\n| Feature | Coverage | Lines | Branches |\n|---|---|---|---|\n" +
		"| auth-login | 75% | 90/120 | 60% |\n| auth-tokens | 75% | 90/120 | 60% |\n| invoices | 90% | 45/50 | 90% |\n" +
		"\nFiles that matched no component: `/opt/other/outside.ts`, `scripts/build.js`\n"
	if string(summary) != wantSummary {
		t.Fatalf("step summary = %q", summary)
	}
	run.requireKeyNowhere()
}

func TestCoverageAction_PushLinksCommitsAndSkipsTheOtherUploads(t *testing.T) {
	release, key := signedCLIRelease(t)
	z := newFakeZensu(t)
	h := newHistory(t)

	run := newActionRun(t, h.dir, release, key, runSetup{
		inputs:    configuredInputs(z, nil),
		eventName: "push",
		event:     map[string]any{"before": "1111111111111111111111111111111111111111", "after": h.billing},
		sha:       h.billing,
	}).run()

	run.requireSteps("config", "install", "commits")
	calls := z.recorded()
	if len(calls) != 1 || calls[0].Path != "/api/ci/commits" || !strings.Contains(calls[0].Body, h.billing) {
		t.Fatalf("calls = %+v", calls)
	}
	want := "::warning::Commit 1111111111111111111111111111111111111111 is not in this checkout, so only " + h.billing + " is checked. Check out with fetch-depth: 0 to cover every commit.\n"
	if !strings.Contains(run.logs["commits"], want) || !strings.Contains(run.logs["commits"], "No feature of this product matches ZEN-999 in commit "+h.billing[:7]+".\n") {
		t.Fatalf("commits output:\n%s", run.logs["commits"])
	}
	run.requireKeyNowhere()
}

func TestCoverageAction_SkipsEverythingWithoutConfiguration(t *testing.T) {
	requireUnixRunner(t)
	key := newSigningKey(t)
	release := newFakeRelease(t, loadAction(t).Inputs["version"].Default, fakeBinary, key)
	z := newFakeZensu(t)
	h := newHistory(t)

	run := newActionRun(t, h.dir, release, key, runSetup{
		inputs:    configuredInputs(z, map[string]string{"api-key": "", "coverage-file": "coverage/coverage-summary.json"}),
		eventName: "pull_request",
		event:     pullRequestEvent(h),
	}).run()

	run.requireSteps("config")
	if run.logs["config"] != "::notice::Zensu is not configured for this run (api-url, api-key or product-id is empty), so nothing is uploaded.\n" {
		t.Fatalf("config output = %q", run.logs["config"])
	}
	if n := len(z.recorded()); n != 0 || release.downloads() != 0 {
		t.Fatalf("%d API calls and %d downloads, want none", n, release.downloads())
	}
}

func TestCoverageAction_StopsWhenTheReleaseCannotBeVerified(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(*fakeRelease)
		want   string
	}{
		{"checksum mismatch", func(r *fakeRelease) { r.write(r.archive, tarball(t, []byte("#!/bin/sh\necho replaced\n"))) }, "error: checksum mismatch for "},
		{"missing signature", func(r *fakeRelease) { r.remove(r.checksums + ".sig") }, "error: could not download the signature zensu_" + strings.TrimPrefix(loadAction(t).Inputs["version"].Default, "v") + "_checksums.txt.sig"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release, key := signedCLIRelease(t)
			tc.tamper(release)
			z := newFakeZensu(t)
			h := newHistory(t)
			run := newActionRun(t, h.dir, release, key, runSetup{inputs: configuredInputs(z, nil), eventName: "push", event: map[string]any{}, sha: h.billing}).run()
			if !slices.Equal(run.ran, []string{"config", "install"}) || run.failed != "install" || !strings.Contains(run.logs["install"], tc.want) {
				t.Fatalf("ran %v failed %q:\n%s", run.ran, run.failed, run.allLogs())
			}
			if n := len(z.recorded()); n != 0 {
				t.Fatalf("%d API calls, want none", n)
			}
		})
	}
}

func TestCoverageAction_InstallsWithInstallPS1OnWindowsRunners(t *testing.T) {
	shells := []string{"pwsh"}
	if runtime.GOOS == "windows" {
		shells = append(shells, "powershell")
	}
	for _, shell := range shells {
		for name, forged := range map[string]bool{"signed release": false, "forged signature": true} {
			t.Run(shell+"/"+name, func(t *testing.T) {
				openssl := requireOpenSSL3(t)
				key := newSigningKey(t)
				release := newWindowsRelease(t, loadAction(t).Inputs["version"].Default, builtCLIBinary(t), key)
				if forged {
					release.write(release.checksums+".sig", []byte(newSigningKey(t).sign(release.read(release.checksums))+"\n"))
				}
				z := newFakeZensu(t)
				h := newHistory(t)
				writeCoverageReport(t, h.dir)
				run := newActionRun(t, h.dir, release, key, runSetup{
					inputs:    configuredInputs(z, map[string]string{"coverage-file": "coverage/coverage-summary.json"}),
					eventName: "pull_request",
					event:     pullRequestEvent(h),
					sha:       h.bump,
					windows:   true,
				})
				run.path = filepath.Dir(openssl) + string(os.PathListSeparator) + run.path
				if shell == "powershell" {
					run.path = pathWithout(run.path, "pwsh.exe")
				}
				reader := powerShells(t)[0]
				before := userPathOf(t, reader)
				run.run()
				if after := userPathOf(t, reader); after != before {
					t.Errorf("the install step changed the user PATH from %q to %q", before, after)
				}
				if strings.Contains(run.logs["install"], "to your PATH") || strings.Contains(run.logs["install"], "to your user PATH") {
					t.Errorf("the install step touched the PATH:\n%s", run.logs["install"])
				}
				if forged {
					want := "error: the signature of " + release.checksums + " matches no zensu release key, so the download may have been tampered with\n"
					if !slices.Equal(run.ran, []string{"config", "install"}) || run.failed != "install" || !strings.HasSuffix(strings.ReplaceAll(run.logs["install"], "\r\n", "\n"), want) {
						t.Fatalf("ran %v failed %q:\n%s", run.ran, run.failed, run.allLogs())
					}
					if n := len(z.recorded()); n != 0 || release.requested(release.archive) {
						t.Fatalf("%d API calls and archive downloaded %v after a forged signature", n, release.requested(release.archive))
					}
					return
				}
				run.requireSteps("config", "install", "commits", "pull-request", "coverage")
				if bin := run.outputs["install"]["bin"]; !strings.HasSuffix(bin, "/zensu.exe") || !strings.Contains(run.logs["install"], "Verified the release signature.") {
					t.Fatalf("bin %q, install output:\n%s", bin, run.logs["install"])
				}
				calls := z.recorded()
				if len(calls) != 3 || calls[0].Path != "/api/ci/commits" || calls[1].Path != "/api/ci/pull-requests" || calls[2].Path != "/api/ci/coverage" || calls[0].Key != actionKey {
					t.Fatalf("calls = %+v", calls)
				}
				jsonEqual(t, calls[2].Body, fmt.Sprintf(`{"productId":%q,"source":"ci","ciRunUrl":%q,"files":[
					{"path":"/opt/other/outside.ts","linesCovered":5,"linesTotal":10},
					{"path":"scripts/build.js","linesCovered":0,"linesTotal":30},
					{"path":"src/auth/login.ts","linesCovered":80,"linesTotal":100,"branchesCovered":6,"branchesTotal":10},
					{"path":"src/auth/token.ts","linesCovered":10,"linesTotal":20},
					{"path":"src/billing/empty.d.ts","linesCovered":0,"linesTotal":0},
					{"path":"src/billing/invoice.ts","linesCovered":45,"linesTotal":50,"branchesCovered":9,"branchesTotal":10}]}`,
					actionProduct, actionRunURL))
				run.requireKeyNowhere()
			})
		}
	}
}

func TestCoverageAction_AFailedCommitLinkFailsTheRunButStillUploadsCoverage(t *testing.T) {
	release, key := signedCLIRelease(t)
	z := newFakeZensu(t)
	z.reject["/api/ci/commits"] = `{"code":"not_found","message":"product not found"}`
	h := newHistory(t)
	writeCoverageReport(t, h.dir)
	run := newActionRun(t, h.dir, release, key, runSetup{
		inputs:    configuredInputs(z, map[string]string{"coverage-file": "coverage/coverage-summary.json"}),
		eventName: "pull_request",
		event:     pullRequestEvent(h),
		sha:       h.billing,
	}).run()
	if !slices.Equal(run.ran, []string{"config", "install", "commits", "pull-request", "coverage"}) || run.failed != "commits" ||
		run.outcomes["pull-request"] != "success" || run.outcomes["coverage"] != "success" {
		t.Fatalf("ran %v failed %q outcomes %v:\n%s", run.ran, run.failed, run.outcomes, run.allLogs())
	}
	if run.logs["commits"] != "::error::product not found (status 404, code not_found)\n" {
		t.Fatalf("commits output = %q", run.logs["commits"])
	}
	calls := z.recorded()
	if len(calls) != 3 || calls[1].Path != "/api/ci/pull-requests" || calls[2].Path != "/api/ci/coverage" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCoverageAction_UploadsCoverageFromAWorkspaceWithoutGit(t *testing.T) {
	release, key := signedCLIRelease(t)
	z := newFakeZensu(t)
	workspace := t.TempDir()
	writeCoverageReport(t, workspace)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(workspace))
	run := newActionRun(t, workspace, release, key, runSetup{
		inputs:    configuredInputs(z, map[string]string{"coverage-file": "coverage/coverage-summary.json"}),
		eventName: "push",
		event:     map[string]any{},
		sha:       "8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d8d",
	})
	run.env["GIT_CEILING_DIRECTORIES"] = filepath.Dir(workspace)
	run.run()
	if !slices.Equal(run.ran, []string{"config", "install", "commits", "coverage"}) || run.failed != "commits" || run.outcomes["coverage"] != "success" {
		t.Fatalf("ran %v failed %q outcomes %v:\n%s", run.ran, run.failed, run.outcomes, run.allLogs())
	}
	if calls := z.recorded(); len(calls) != 1 || calls[0].Path != "/api/ci/coverage" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestCoverageAction_RunsInTheWorkingDirectoryOfACheckoutInASubdirectory(t *testing.T) {
	release, key := signedCLIRelease(t)
	z := newFakeZensu(t)
	h := newHistory(t)
	checkout, err := filepath.EvalSymlinks(h.dir)
	if err != nil {
		t.Fatal(err)
	}
	writeCoverageReport(t, checkout)
	run := newActionRun(t, filepath.Dir(checkout), release, key, runSetup{
		inputs:    configuredInputs(z, map[string]string{"coverage-file": "coverage/coverage-summary.json", "working-directory": filepath.Base(checkout)}),
		eventName: "push",
		event:     map[string]any{"before": h.base},
		sha:       h.billing,
	}).run()

	run.requireSteps("config", "install", "commits", "coverage")
	calls := z.recorded()
	if len(calls) != 2 {
		t.Fatalf("%d API calls, want 2: %+v", len(calls), calls)
	}
	for _, hash := range []string{h.billing, h.bump, h.first} {
		if !strings.Contains(calls[0].Body, hash) {
			t.Fatalf("commits body lacks %s: %s", hash, calls[0].Body)
		}
	}
	jsonEqual(t, calls[1].Body, fmt.Sprintf(`{"productId":%q,"source":"ci","ciRunUrl":%q,"files":[
		{"path":"/opt/other/outside.ts","linesCovered":5,"linesTotal":10},
		{"path":"scripts/build.js","linesCovered":0,"linesTotal":30},
		{"path":"src/auth/login.ts","linesCovered":80,"linesTotal":100,"branchesCovered":6,"branchesTotal":10},
		{"path":"src/auth/token.ts","linesCovered":10,"linesTotal":20},
		{"path":"src/billing/empty.d.ts","linesCovered":0,"linesTotal":0},
		{"path":"src/billing/invoice.ts","linesCovered":45,"linesTotal":50,"branchesCovered":9,"branchesTotal":10}]}`,
		actionProduct, actionRunURL))
	run.requireKeyNowhere()
}

func TestCoverageAction_StepsReadInputsOnlyThroughEnv(t *testing.T) {
	action := loadAction(t)
	if action.Runs.Using != "composite" {
		t.Fatalf("runs.using = %q", action.Runs.Using)
	}
	for name, input := range action.Inputs {
		if wantRequired := slices.Contains([]string{"api-key", "api-url", "product-id"}, name); input.Required != wantRequired {
			t.Errorf("input %s required = %v", name, input.Required)
		}
		if input.Description == "" {
			t.Errorf("input %s has no description", name)
		}
	}
	if got := action.Inputs["coverage-format"].Default; got != "json-summary" {
		t.Errorf("coverage-format default = %q", got)
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(action.Inputs["version"].Default) {
		t.Errorf("version default %q is not a release tag", action.Inputs["version"].Default)
	}
	envValue := regexp.MustCompile(`^(\$\{\{ (inputs\.[a-z-]+|steps\.install\.outputs\.bin) \}\}|[A-Za-z0-9]+)$`)
	uploads := 0
	for _, step := range action.Runs.Steps {
		if step.Shell != "bash" || step.Uses != "" {
			t.Errorf("step %s is not a bash run step", step.ID)
		}
		if strings.Contains(step.Run, "${{") {
			t.Errorf("step %s interpolates an expression into its script", step.ID)
		}
		for name, value := range step.Env {
			if !envValue.MatchString(value) {
				t.Errorf("step %s env %s = %q", step.ID, name, value)
			}
			if value == "${{ inputs.api-key }}" && name != "ZENSU_API_KEY" {
				t.Errorf("step %s passes the key as %s", step.ID, name)
			}
		}
		if strings.Contains(step.Run, "jq") || strings.Contains(step.Run, "X-API-Key") || strings.Contains(step.Run, "--api-key ") {
			t.Errorf("step %s uses jq or puts the key on a command line:\n%s", step.ID, step.Run)
		}
		if strings.Contains(step.Run, "ZENSU_API_KEY") && step.ID != "config" {
			uploads++
			if !strings.Contains(step.Run, `printf '%s' "${ZENSU_API_KEY}" | "${ZENSU_BIN}"`) || !strings.Contains(step.Run, "--api-key-stdin") {
				t.Errorf("step %s does not pipe the key into --api-key-stdin:\n%s", step.ID, step.Run)
			}
		}
	}
	if uploads != 3 {
		t.Errorf("%d upload steps read the key, want 3", uploads)
	}
	for _, step := range action.Runs.Steps[2:] {
		if step.WorkingDirectory != "${{ inputs.working-directory }}" {
			t.Errorf("step %s runs in %q, not the working-directory input", step.ID, step.WorkingDirectory)
		}
		if step.ID != "commits" && !strings.HasPrefix(step.If, "${{ !cancelled() && steps.install.outcome == 'success' && ") {
			t.Errorf("step %s does not run after a failed commit link: if: %s", step.ID, step.If)
		}
	}
	install := action.Runs.Steps[1]
	if install.ID != "install" || install.Env["ZENSU_REQUIRE_SIGNATURE"] != "1" || install.Env["ZENSU_NO_MODIFY_PATH"] != "1" ||
		!strings.Contains(install.Run, `"${GITHUB_ACTION_PATH}/../../install.sh"`) || !strings.Contains(install.Run, `"${GITHUB_ACTION_PATH}/../../install.ps1"`) {
		t.Errorf("install step = %+v", install)
	}
}

type workflow struct {
	Jobs map[string]struct {
		Steps []actionStep `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestCoverageAction_ReleaseRefusesATagTheActionDoesNotInstall(t *testing.T) {
	requireUnixRunner(t)
	var release workflow
	loadYAML(t, filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"), &release)
	steps := release.Jobs["goreleaser"].Steps
	guard := slices.IndexFunc(steps, func(s actionStep) bool { return s.Name == "Check that the coverage action installs this release" })
	goreleaser := slices.IndexFunc(steps, func(s actionStep) bool { return strings.HasPrefix(s.Uses, "goreleaser/goreleaser-action@") })
	if guard < 0 || goreleaser < 0 || guard > goreleaser {
		t.Fatalf("the version guard (step %d) must run before goreleaser (step %d)", guard, goreleaser)
	}
	if steps[guard].Env["TAG"] != "${{ github.ref_name }}" {
		t.Fatalf("guard env = %v", steps[guard].Env)
	}
	pinned := loadAction(t).Inputs["version"].Default
	for _, tc := range []struct {
		tag  string
		code int
		want string
	}{
		{pinned, 0, "The coverage action installs " + pinned + " by default.\n"},
		{"v9.9.9", 1, "::error file=actions/coverage/action.yml::The coverage action installs " + pinned + " by default, but this release is v9.9.9. Do not move this tag: set the default of its version input to the next version on main and tag that commit instead.\n"},
	} {
		cmd := exec.Command("bash", "--noprofile", "--norc", "-eo", "pipefail", "-c", steps[guard].Run)
		cmd.Dir = repoRoot(t)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "TAG=" + tc.tag}
		out, err := cmd.CombinedOutput()
		code := 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		if code != tc.code || string(out) != tc.want {
			t.Errorf("tag %s: exit %d output %q", tc.tag, code, out)
		}
	}
}

func TestCoverageAction_CIRunsTheActionWithoutAKey(t *testing.T) {
	var ci workflow
	loadYAML(t, filepath.Join(repoRoot(t), ".github", "workflows", "ci.yml"), &ci)
	for _, job := range ci.Jobs {
		for _, step := range job.Steps {
			if step.Uses == "./actions/coverage" && step.With["api-key"] == "" && step.With["api-url"] != "" && step.With["product-id"] != "" && step.With["coverage-file"] != "" {
				return
			}
		}
	}
	t.Fatal("no CI job runs uses: ./actions/coverage with an empty api-key")
}
