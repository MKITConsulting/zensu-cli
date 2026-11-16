package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const coverageGoldenBody = `{
	"productId": "` + ciTestProduct + `",
	"source": "ci",
	"ciRunUrl": "https://github.com/acme/shop/actions/runs/4242",
	"files": [
		{"path": "/opt/other/outside.ts", "linesCovered": 5, "linesTotal": 10},
		{"path": "scripts/build.js", "linesCovered": 0, "linesTotal": 30},
		{"path": "src/auth/login.ts", "linesCovered": 80, "linesTotal": 100, "branchesCovered": 6, "branchesTotal": 10},
		{"path": "src/auth/token.ts", "linesCovered": 10, "linesTotal": 20},
		{"path": "src/billing/empty.d.ts", "linesCovered": 0, "linesTotal": 0},
		{"path": "src/billing/invoice.ts", "linesCovered": 45, "linesTotal": 50, "branchesCovered": 9, "branchesTotal": 10}
	]
}`

const coverageAnswerBody = `{"mappedFeatures":3,"entries":[` +
	`{"featureId":"f1","slug":"auth-login","coveragePercent":75,"linesCovered":90,"linesTotal":120,"branchCoverage":60},` +
	`{"featureId":"f2","slug":"auth-tokens","coveragePercent":75,"linesCovered":90,"linesTotal":120,"branchCoverage":60},` +
	`{"featureId":"f3","slug":"invoices","coveragePercent":90,"linesCovered":45,"linesTotal":50,"branchCoverage":90}],` +
	`"unmappedPaths":["/opt/other/outside.ts","scripts/build.js"]}`

const coverageGoldenSummary = "## Zensu coverage\n\n" +
	"| Feature | Coverage | Lines | Branches |\n|---|---|---|---|\n" +
	"| auth-login | 75% | 90/120 | 60% |\n" +
	"| auth-tokens | 75% | 90/120 | 60% |\n" +
	"| invoices | 90% | 45/50 | 90% |\n" +
	"\nFiles that matched no component: `/opt/other/outside.ts`, `scripts/build.js`\n"

func coverageAPI(t *testing.T, answer string) (*Factory, *fakeAPI) {
	t.Helper()
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST /api/ci/coverage": reply(http.StatusOK, answer)})
	f, _ := testFactory(srv)
	return f, api
}

func TestCICoverage_SendsEveryFileRelativeToTheRoot(t *testing.T) {
	clearGitHubEnv(t)
	f, api := coverageAPI(t, coverageAnswerBody)
	res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json",
		"--root", "/workspace/shop/", "--ci-run-url", "https://github.com/acme/shop/actions/runs/4242")
	if res.err != nil {
		t.Fatalf("coverage: %v\n%s", res.err, res.stderr)
	}
	requireJSONEqual(t, api.only(t, http.MethodPost, "/api/ci/coverage").Raw, coverageGoldenBody)
	if res.stdout != "Zensu recorded coverage for 3 feature(s); 2 of 6 file(s) matched no component.\n" || res.stderr != "" {
		t.Fatalf("stdout = %q stderr = %q", res.stdout, res.stderr)
	}
}

func TestCICoverage_GitHubSuppliesRootRunURLAndSummary(t *testing.T) {
	clearGitHubEnv(t)
	summary := writeTestFile(t, "summary.md", "## Earlier step\n")
	t.Setenv("GITHUB_WORKSPACE", "/workspace/shop")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com/")
	t.Setenv("GITHUB_REPOSITORY", "acme/shop")
	t.Setenv("GITHUB_RUN_ID", "4242")
	t.Setenv("GITHUB_STEP_SUMMARY", summary)
	f, api := coverageAPI(t, coverageAnswerBody)

	res := runCI(t, f, "", "coverage", "--github", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json")

	if res.err != nil {
		t.Fatalf("coverage: %v\n%s", res.err, res.stderr)
	}
	requireJSONEqual(t, api.only(t, http.MethodPost, "/api/ci/coverage").Raw, coverageGoldenBody)
	data, err := os.ReadFile(summary)
	if err != nil || string(data) != "## Earlier step\n"+coverageGoldenSummary {
		t.Fatalf("step summary = %q (%v)", data, err)
	}
	if res.stdout != "Zensu recorded coverage for 3 feature(s); 2 of 6 file(s) matched no component.\n" || res.stderr != "" {
		t.Fatalf("stdout = %q stderr = %q", res.stdout, res.stderr)
	}
}

func TestCICoverage_ExplicitFlagsWinOverGitHub(t *testing.T) {
	clearGitHubEnv(t)
	stepSummary := writeTestFile(t, "step.md", "")
	ownSummary := filepath.Join(t.TempDir(), "own.md")
	t.Setenv("GITHUB_WORKSPACE", "/workspace")
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/shop")
	t.Setenv("GITHUB_RUN_ID", "4242")
	t.Setenv("GITHUB_STEP_SUMMARY", stepSummary)
	f, api := coverageAPI(t, coverageAnswerBody)

	res := runCI(t, f, "", "coverage", "--github", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json",
		"--root", "/workspace/shop", "--ci-run-url", "https://ci.example.test/runs/9", "--markdown-summary", ownSummary)

	if res.err != nil {
		t.Fatalf("coverage: %v", res.err)
	}
	var body struct {
		CIRunURL string `json:"ciRunUrl"`
		Files    []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(api.only(t, http.MethodPost, "/api/ci/coverage").Raw), &body); err != nil {
		t.Fatal(err)
	}
	if body.CIRunURL != "https://ci.example.test/runs/9" || body.Files[2].Path != "src/auth/login.ts" {
		t.Fatalf("body = %+v, want the explicit root and run URL", body)
	}
	if data, _ := os.ReadFile(stepSummary); len(data) != 0 {
		t.Fatalf("GITHUB_STEP_SUMMARY was written although --markdown-summary was given: %q", data)
	}
	if data, err := os.ReadFile(ownSummary); err != nil || string(data) != coverageGoldenSummary {
		t.Fatalf("summary = %q (%v)", data, err)
	}
}

func coverageReport(t *testing.T, root string, paths ...string) string {
	t.Helper()
	report := map[string]any{"total": map[string]any{"lines": map[string]any{"total": 2, "covered": 1}}}
	for _, p := range paths {
		if !filepath.IsAbs(p) && root != "" {
			p = filepath.Join(root, p)
		}
		report[p] = map[string]any{"lines": map[string]any{"total": 2, "covered": 1}}
	}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return writeTestFile(t, "coverage-summary.json", string(data))
}

func sentPaths(t *testing.T, api *fakeAPI) []string {
	t.Helper()
	var body struct {
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(api.only(t, http.MethodPost, "/api/ci/coverage").Raw), &body); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, f := range body.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

func TestCICoverage_RootDefaultsToTheGitTopLevel(t *testing.T) {
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	top, err := exec.Command("git", "-C", repo.dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}
	root := strings.TrimSpace(string(top))
	sub := filepath.Join(repo.dir, "frontend")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)
	report := coverageReport(t, root, "frontend/src/auth/login.ts", "src/cart/total.ts")
	f, api := coverageAPI(t, coverageAnswerBody)

	if res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", report); res.err != nil {
		t.Fatalf("coverage: %v", res.err)
	}
	if got := sentPaths(t, api); strings.Join(got, ",") != "frontend/src/auth/login.ts,src/cart/total.ts" {
		t.Fatalf("paths = %q", got)
	}
}

func TestCICoverage_RelativeRootFollowsTheWorkingDirectory(t *testing.T) {
	clearGitHubEnv(t)
	base := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(base))
	resolved, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "frontend"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(base)
	report := coverageReport(t, "", filepath.Join(resolved, "frontend", "src", "auth", "login.ts"), filepath.Join(resolved, "README.md"), "relative/kept.ts")
	f, api := coverageAPI(t, coverageAnswerBody)

	if res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", report, "--root", "frontend"); res.err != nil {
		t.Fatalf("coverage: %v", res.err)
	}
	want := filepath.ToSlash(filepath.Join(resolved, "README.md"))
	if got := sentPaths(t, api); strings.Join(got, ",") != want+",src/auth/login.ts,relative/kept.ts" {
		t.Fatalf("paths = %q", got)
	}

	f, api = coverageAPI(t, coverageAnswerBody)
	if res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", report); res.err != nil {
		t.Fatalf("coverage without --root: %v", res.err)
	}
	if got := sentPaths(t, api); strings.Join(got, ",") != "README.md,frontend/src/auth/login.ts,relative/kept.ts" {
		t.Fatalf("paths outside a git repository = %q, want them relative to the working directory", got)
	}
}

func TestCICoverage_JSONPrintsTheAnswer(t *testing.T) {
	clearGitHubEnv(t)
	f, _ := coverageAPI(t, coverageAnswerBody)
	res := runCI(t, f, "", "coverage", "--json", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json", "--root", "/workspace/shop")
	if res.err != nil {
		t.Fatalf("coverage: %v", res.err)
	}
	requireJSONEqual(t, res.stdout, coverageAnswerBody)
}

func TestCICoverage_SummaryCoversEmptyAndLongResults(t *testing.T) {
	clearGitHubEnv(t)
	var unmapped []string
	for i := range 23 {
		unmapped = append(unmapped, fmt.Sprintf(`"docs/page%02d.ts"`, i))
	}
	for _, tc := range []struct {
		name    string
		answer  string
		summary string
		stdout  string
		stderr  string
	}{
		{
			name:    "nothing mapped",
			answer:  `{"mappedFeatures":0,"entries":[],"unmappedPaths":["docs/guide.ts","scripts/build.js"]}`,
			summary: "## Zensu coverage\n\nNo feature coverage changed.\n\nFiles that matched no component: `docs/guide.ts`, `scripts/build.js`\n",
			stdout:  "Zensu recorded coverage for 0 feature(s); 2 of 6 file(s) matched no component.\n",
			stderr:  "warning: No file in the coverage report matched a component of this product, so no feature coverage changed.\n",
		},
		{
			name:    "every file mapped",
			answer:  `{"mappedFeatures":1,"entries":[{"slug":"a|b","coveragePercent":33.33}],"unmappedPaths":[]}`,
			summary: "## Zensu coverage\n\n| Feature | Coverage | Lines | Branches |\n|---|---|---|---|\n| a\\|b | 33.33% | n/a | n/a |\n",
			stdout:  "Zensu recorded coverage for 1 feature(s); 0 of 6 file(s) matched no component.\n",
		},
		{
			name:    "answer text cannot add Markdown",
			answer:  "{\"mappedFeatures\":1,\"entries\":[{\"slug\":\"x](https://evil.example) ![i](https://evil.example/p.png) <b>&amp;\",\"coveragePercent\":50}],\"unmappedPaths\":[\"a` [Sign in](https://evil.example) `b\",\"``edge\"]}",
			summary: "## Zensu coverage\n\n| Feature | Coverage | Lines | Branches |\n|---|---|---|---|\n| x\\](https://evil.example) \\!\\[i\\](https://evil.example/p.png) \\<b\\>\\&amp; | 50% | n/a | n/a |\n\nFiles that matched no component: ``a` [Sign in](https://evil.example) `b``, ``` ``edge ```\n",
			stdout:  "Zensu recorded coverage for 1 feature(s); 2 of 6 file(s) matched no component.\n",
		},
		{
			name:    "more than twenty unmapped files",
			answer:  `{"mappedFeatures":0,"entries":[],"unmappedPaths":[` + strings.Join(unmapped, ",") + `]}`,
			summary: "## Zensu coverage\n\nNo feature coverage changed.\n\nFiles that matched no component: `docs/page00.ts`, `docs/page01.ts`, `docs/page02.ts`, `docs/page03.ts`, `docs/page04.ts`, `docs/page05.ts`, `docs/page06.ts`, `docs/page07.ts`, `docs/page08.ts`, `docs/page09.ts`, `docs/page10.ts`, `docs/page11.ts`, `docs/page12.ts`, `docs/page13.ts`, `docs/page14.ts`, `docs/page15.ts`, `docs/page16.ts`, `docs/page17.ts`, `docs/page18.ts`, `docs/page19.ts` and 3 more\n",
			stdout:  "Zensu recorded coverage for 0 feature(s); 23 of 6 file(s) matched no component.\n",
			stderr:  "warning: No file in the coverage report matched a component of this product, so no feature coverage changed.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			summary := filepath.Join(t.TempDir(), "summary.md")
			f, _ := coverageAPI(t, tc.answer)
			res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json", "--root", "/workspace/shop", "--markdown-summary", summary)
			if res.err != nil {
				t.Fatalf("coverage: %v", res.err)
			}
			data, err := os.ReadFile(summary)
			if err != nil || string(data) != tc.summary {
				t.Fatalf("summary = %q (%v)\nwant %q", data, err, tc.summary)
			}
			if res.stdout != tc.stdout || res.stderr != tc.stderr {
				t.Fatalf("stdout = %q stderr = %q", res.stdout, res.stderr)
			}
		})
	}
}

func TestCICoverage_WarnsAsAnnotationWithGitHub(t *testing.T) {
	clearGitHubEnv(t)
	f, _ := coverageAPI(t, `{"mappedFeatures":0,"entries":[],"unmappedPaths":["docs/guide.ts"]}`)
	res := runCI(t, f, "", "coverage", "--github", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json", "--root", "/workspace/shop")
	if res.err != nil {
		t.Fatalf("coverage: %v", res.err)
	}
	if res.stderr != "::warning::No file in the coverage report matched a component of this product, so no feature coverage changed.\n" {
		t.Fatalf("stderr = %q", res.stderr)
	}
}

func TestCICoverage_AReportWithoutFilesUploadsNothing(t *testing.T) {
	clearGitHubEnv(t)
	report := writeTestFile(t, "total-only.json", `{"total":{"lines":{"total":2,"covered":1}}}`)
	for _, tc := range []struct {
		args   []string
		stdout string
		stderr string
	}{
		{nil, "", "warning: " + report + " lists no files, so no coverage is uploaded.\n"},
		{[]string{"--github"}, "", "::warning::" + report + " lists no files, so no coverage is uploaded.\n"},
		{[]string{"--json"}, "{\n  \"mappedFeatures\": 0,\n  \"entries\": [],\n  \"unmappedPaths\": []\n}\n", "warning: " + report + " lists no files, so no coverage is uploaded.\n"},
	} {
		f, api := coverageAPI(t, coverageAnswerBody)
		res := runCI(t, f, "", append([]string{"coverage", "--product", ciTestProduct, "--file", report}, tc.args...)...)
		if res.err != nil || res.stdout != tc.stdout || res.stderr != tc.stderr {
			t.Errorf("%v: err %v stdout %q stderr %q", tc.args, res.err, res.stdout, res.stderr)
		}
		if n := len(api.calls()); n != 0 {
			t.Errorf("%v: %d requests, want none", tc.args, n)
		}
	}
}

func TestCICoverage_RefusesMoreFilesThanOneUploadTakes(t *testing.T) {
	clearGitHubEnv(t)
	var b strings.Builder
	b.WriteString(`{"total":{"lines":{"total":2,"covered":1}}`)
	for i := range coverageMaxFiles + 1 {
		fmt.Fprintf(&b, `,"src/generated/file%05d.ts":{"lines":{"total":2,"covered":1}}`, i)
	}
	b.WriteString("}")
	report := writeTestFile(t, "huge.json", b.String())
	f, api := coverageAPI(t, coverageAnswerBody)
	res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", report)
	if res.err == nil || res.err.Error() != report+" lists 10001 files; Zensu accepts at most 10000 per upload" {
		t.Fatalf("error = %v", res.err)
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("%d requests, want none", n)
	}
}

func TestCICoverage_RejectsUnusableInputsBeforeUploading(t *testing.T) {
	clearGitHubEnv(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	for _, tc := range []struct {
		name   string
		report string
		args   []string
		want   string
	}{
		{"no --file", "", nil, "--file is required"},
		{"unsupported format", "testdata/coverage-summary.json", []string{"--format", "lcov"}, `coverage format "lcov" is not supported; use json-summary`},
		{"missing file", missing, nil, "coverage file not found: " + missing},
		{"directory", dir, nil, dir + " is not a regular file"},
		{"lcov text", "lcov.info", nil, "%s is not an Istanbul json-summary report"},
		{"array", "[]", nil, "%s is not an Istanbul json-summary report"},
		{"no total", `{"src/a.ts":{"lines":{"total":2,"covered":1}}}`, nil, "%s is not an Istanbul json-summary report"},
		{"two values", `{"total":{}}{"total":{}}`, nil, "%s is not an Istanbul json-summary report"},
		{"no lines", `{"total":{},"src/a.ts":{"statements":{"total":2,"covered":1}}}`, nil, `%s is not an Istanbul json-summary report: the entry "src/a.ts" has no whole-number lines.covered and lines.total`},
		{"fractional count", `{"total":{},"src/a.ts":{"lines":{"total":2.5,"covered":1}}}`, nil, `%s is not an Istanbul json-summary report: the entry "src/a.ts" has no whole-number lines.covered and lines.total`},
		{"negative count", `{"total":{},"src/a.ts":{"lines":{"total":2,"covered":-1}}}`, nil, `%s is not an Istanbul json-summary report: the entry "src/a.ts" has no whole-number lines.covered and lines.total`},
		{"string branches", `{"total":{},"src/a.ts":{"lines":{"total":2,"covered":1},"branches":{"total":"4","covered":1}}}`, nil, `%s is not an Istanbul json-summary report: the entry "src/a.ts" has no whole-number branches.total`},
		{"branches without covered", `{"total":{},"src/a.ts":{"lines":{"total":2,"covered":1},"branches":{"total":4}}}`, nil, `%s is not an Istanbul json-summary report: the entry "src/a.ts" has no whole-number branches.covered`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := tc.report
			if strings.HasPrefix(report, "{") || strings.HasPrefix(report, "[") || report == "lcov.info" {
				content := report
				if report == "lcov.info" {
					content = "TN:\nSF:src/auth/login.ts\nend_of_record\n"
				}
				report = writeTestFile(t, "report.json", content)
			}
			f, api := coverageAPI(t, coverageAnswerBody)
			args := []string{"coverage", "--product", ciTestProduct}
			if report != "" {
				args = append(args, "--file", report)
			}
			res := runCI(t, f, "", append(args, tc.args...)...)
			want := tc.want
			if strings.Contains(want, "%s") {
				want = fmt.Sprintf(want, report)
			}
			if res.err == nil || res.err.Error() != want {
				t.Fatalf("error = %v\nwant %q", res.err, want)
			}
			if n := len(api.calls()); n != 0 {
				t.Fatalf("%d requests, want none", n)
			}
		})
	}
}

func TestCICoverage_KeepsDocumentOrderAndTheLastDuplicate(t *testing.T) {
	clearGitHubEnv(t)
	report := writeTestFile(t, "report.json", "\xef\xbb\xbf"+`{"src/z.ts":{"lines":{"total":4,"covered":1}},"total":{},"src/a.ts":{"lines":{"total":2,"covered":2},"branches":{"total":0,"covered":0}},"src/z.ts":{"lines":{"total":4,"covered":3},"branches":{"total":2,"covered":1}}}`)
	f, api := coverageAPI(t, coverageAnswerBody)
	if res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", report, "--root", "/workspace"); res.err != nil {
		t.Fatalf("coverage: %v", res.err)
	}
	requireJSONEqual(t, api.only(t, http.MethodPost, "/api/ci/coverage").Raw, `{"productId":"`+ciTestProduct+`","source":"ci","files":[`+
		`{"path":"src/z.ts","linesCovered":3,"linesTotal":4,"branchesCovered":1,"branchesTotal":2},`+
		`{"path":"src/a.ts","linesCovered":2,"linesTotal":2}]}`)
}

func TestCICoverage_SurfacesAServerRejection(t *testing.T) {
	clearGitHubEnv(t)
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/ci/coverage": reply(http.StatusBadRequest, `{"code":"bad_request","message":"files[1].path repeats files[0].path"}`),
	})
	f, _ := testFactory(srv)
	summary := filepath.Join(t.TempDir(), "summary.md")
	res := runCI(t, f, "", "coverage", "--product", ciTestProduct, "--file", "testdata/coverage-summary.json", "--root", "/workspace/shop", "--markdown-summary", summary)
	if res.err == nil || res.stderr != "error: files[1].path repeats files[0].path (status 400, code bad_request)\n" || res.stdout != "" {
		t.Fatalf("err %v stdout %q stderr %q", res.err, res.stdout, res.stderr)
	}
	if _, err := os.Stat(summary); !os.IsNotExist(err) {
		t.Fatal("a rejected upload wrote a summary")
	}
}
