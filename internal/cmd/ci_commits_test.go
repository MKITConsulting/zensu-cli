package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type ciGitRepo struct {
	t   *testing.T
	dir string
}

func newCIGitRepo(t *testing.T) *ciGitRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CEILING_DIRECTORIES", "")
	r := &ciGitRepo{t: t, dir: t.TempDir()}
	r.git(nil, "init", "-q", "-b", "main")
	r.git(nil, "config", "user.name", "Ada Lovelace")
	r.git(nil, "config", "user.email", "ada@example.test")
	r.git(nil, "config", "commit.gpgsign", "false")
	return r
}

func (r *ciGitRepo) git(stdin *strings.Reader, args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *ciGitRepo) commitAt(message, date string, extra ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"commit", "-q", "--allow-empty", "-m", message}, extra...)...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git commit: %v\n%s", err, out)
	}
	return r.git(nil, "rev-parse", "HEAD")
}

func (r *ciGitRepo) commit(message string) string {
	return r.commitAt(message, "2026-10-01T09:00:00+02:00")
}

type ciHistory struct {
	repo                             *ciGitRepo
	base, first, typo, bump, billing string
}

func newCIHistory(t *testing.T) ciHistory {
	t.Helper()
	repo := newCIGitRepo(t)
	h := ciHistory{repo: repo}
	h.base = repo.commitAt("chore: initial import", "2026-10-01T09:00:00+02:00")
	h.first = repo.commitAt("feat(auth): rotate refresh tokens [ZEN-42]", "2026-10-02T10:15:00+02:00")
	h.typo = repo.commitAt("docs: fix a typo", "2026-10-03T11:00:00+02:00")
	h.bump = repo.commitAt("chore: bump the token library\n\nRefs [ZEN-42]", "2026-10-04T08:45:00+02:00")
	h.billing = repo.commitAt("fix(billing): round invoice totals [zen-invoice-rounding]\n\nAlso closes [ZEN-999].", "2026-10-05T12:30:00+02:00")
	t.Chdir(repo.dir)
	return h
}

func githubEvent(t *testing.T, name string, event map[string]any) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_EVENT_NAME", name)
	t.Setenv("GITHUB_EVENT_PATH", writeTestFile(t, "event.json", string(data)))
}

func commitsAPI(t *testing.T) (*Factory, *fakeAPI) {
	t.Helper()
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST /api/ci/commits": commitsReply})
	f, _ := testFactory(srv)
	return f, api
}

func sentHashes(t *testing.T, api *fakeAPI) []string {
	t.Helper()
	var hashes []string
	for _, call := range api.calls() {
		var body struct {
			Commits []struct {
				Hash string `json:"hash"`
			} `json:"commits"`
		}
		if err := json.Unmarshal([]byte(call.Raw), &body); err != nil {
			t.Fatal(err)
		}
		for _, c := range body.Commits {
			hashes = append(hashes, c.Hash)
		}
	}
	return hashes
}

func TestCICommits_UploadsEveryCommitThatReferencesAFeature(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	githubEvent(t, "push", map[string]any{"before": h.base, "after": h.billing})
	t.Setenv("GITHUB_SHA", h.billing)
	f, api := commitsAPI(t)

	res := runCI(t, f, "", "commits", "--github", "--product", ciTestProduct)

	if res.err != nil {
		t.Fatalf("commits: %v\n%s", res.err, res.stderr)
	}
	call := api.only(t, http.MethodPost, "/api/ci/commits")
	requireJSONEqual(t, call.Raw, fmt.Sprintf(`{
		"productId": %q,
		"commits": [
			{"hash": %q, "message": "fix(billing): round invoice totals [zen-invoice-rounding]\n\nAlso closes [ZEN-999].", "author": "Ada Lovelace", "timestamp": "2026-10-05T12:30:00+02:00"},
			{"hash": %q, "message": "chore: bump the token library\n\nRefs [ZEN-42]", "author": "Ada Lovelace", "timestamp": "2026-10-04T08:45:00+02:00"},
			{"hash": %q, "message": "feat(auth): rotate refresh tokens [ZEN-42]", "author": "Ada Lovelace", "timestamp": "2026-10-02T10:15:00+02:00"}
		]
	}`, ciTestProduct, h.billing, h.bump, h.first))
	want := "Zensu linked 3 feature reference(s) from 3 commit(s).\nNo feature of this product matches ZEN-999 in commit " + h.billing[:7] + ".\n"
	if res.stdout != want || res.stderr != "" {
		t.Fatalf("stdout = %q stderr = %q", res.stdout, res.stderr)
	}
}

func TestCICommits_RangeFollowsTheGitHubEvent(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	absent := "1111111111111111111111111111111111111111"
	for _, tc := range []struct {
		name    string
		event   string
		payload map[string]any
		sha     string
		want    []string
		warning string
	}{
		{"a pull request covers base..head", "pull_request",
			map[string]any{"pull_request": map[string]any{"base": map[string]any{"sha": h.base}, "head": map[string]any{"sha": h.bump}}},
			h.billing, []string{h.bump, h.first}, ""},
		{"a pull request checkout without its head uses HEAD", "pull_request",
			map[string]any{"pull_request": map[string]any{"base": map[string]any{"sha": h.base}, "head": map[string]any{"sha": absent}}},
			h.first, []string{h.billing, h.bump, h.first}, ""},
		{"a new branch covers only the pushed head", "push",
			map[string]any{"before": "0000000000000000000000000000000000000000", "after": h.billing},
			h.billing, []string{h.billing}, ""},
		{"a base outside the checkout leaves the head with a warning", "push",
			map[string]any{"before": absent, "after": h.billing},
			h.billing, []string{h.billing},
			"::warning::Commit " + absent + " is not in this checkout, so only " + h.billing + " is checked. Check out with fetch-depth: 0 to cover every commit.\n"},
		{"other events cover GITHUB_SHA, not HEAD", "workflow_dispatch",
			map[string]any{"before": h.base},
			h.first, []string{h.first}, ""},
		{"a GITHUB_SHA outside the checkout falls back to HEAD", "push",
			map[string]any{"before": h.bump},
			absent, []string{h.billing}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			githubEvent(t, tc.event, tc.payload)
			t.Setenv("GITHUB_SHA", tc.sha)
			f, api := commitsAPI(t)
			res := runCI(t, f, "", "commits", "--github", "--product", ciTestProduct)
			if res.err != nil {
				t.Fatalf("commits: %v\n%s", res.err, res.stderr)
			}
			if got := sentHashes(t, api); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("hashes = %v, want %v", got, tc.want)
			}
			if res.stderr != tc.warning {
				t.Fatalf("stderr = %q, want %q", res.stderr, tc.warning)
			}
		})
	}
}

func TestCICommits_IgnoresTheGitHubContextWithoutTheGitHubFlag(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	githubEvent(t, "push", map[string]any{"before": "1111111111111111111111111111111111111111", "after": h.bump})
	t.Setenv("GITHUB_SHA", h.bump)
	f, api := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--product", ciTestProduct)
	if res.err != nil {
		t.Fatalf("commits: %v", res.err)
	}
	if got := sentHashes(t, api); strings.Join(got, ",") != h.billing {
		t.Fatalf("hashes = %v, want only HEAD %s", got, h.billing)
	}
	if res.stderr != "" {
		t.Fatalf("stderr = %q, want no warning", res.stderr)
	}
}

func TestCICommits_WarnsInPlainTextWithoutGitHub(t *testing.T) {
	clearGitHubEnv(t)
	c := newShallowMergeClone(t)
	f, api := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--product", ciTestProduct, "--range", c.base+".."+c.head)
	if res.err != nil {
		t.Fatalf("commits: %v\n%s", res.err, res.stderr)
	}
	if got := sentHashes(t, api); !slices.Equal(got, []string{c.head}) {
		t.Fatalf("sent %q, want only the head %s that the shallow checkout holds", got, c.head)
	}
	want := "warning: This checkout is shallow, so " + c.base + ".." + c.head + " may miss the commits before " + c.head[:7] + ". Check out with fetch-depth: 0 to cover every commit.\n"
	if res.stderr != want || strings.Contains(res.stdout, "::") {
		t.Fatalf("stdout = %q stderr = %q\nwant stderr %q", res.stdout, res.stderr, want)
	}
}

func TestCICommits_ExplicitRangeWinsOverTheEvent(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	githubEvent(t, "push", map[string]any{"before": h.base})
	t.Setenv("GITHUB_SHA", h.billing)
	f, api := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--github", "--product", ciTestProduct, "--range", h.first+".."+h.bump)
	if res.err != nil {
		t.Fatalf("commits: %v", res.err)
	}
	if got := sentHashes(t, api); strings.Join(got, ",") != h.bump {
		t.Fatalf("hashes = %v, want only %s", got, h.bump)
	}
}

func TestCICommits_SkipsARangeWithoutReferences(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	githubEvent(t, "push", map[string]any{"before": h.first, "after": h.typo})
	t.Setenv("GITHUB_SHA", h.typo)
	f, api := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--github", "--product", ciTestProduct)
	if res.err != nil || res.stdout != "No commit in "+h.first+".."+h.typo+" references a feature.\n" {
		t.Fatalf("err %v stdout %q", res.err, res.stdout)
	}
	f, _ = commitsAPI(t)
	res = runCI(t, f, "", "commits", "--github", "--json", "--product", ciTestProduct)
	if res.err != nil || res.stdout != "{\n  \"processed\": 0,\n  \"links\": []\n}\n" {
		t.Fatalf("--json: err %v stdout %q", res.err, res.stdout)
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("%d requests, want none", n)
	}
}

func TestCICommits_SendsAtMostAThousandCommitsPerRequest(t *testing.T) {
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	var stream strings.Builder
	for i := 1; i <= commitBatchSize+1; i++ {
		message := fmt.Sprintf("feat: change %d [ZEN-42]\n", i)
		fmt.Fprintf(&stream, "commit refs/heads/main\nauthor Ada Lovelace <ada@example.test> %d +0000\ncommitter Ada Lovelace <ada@example.test> %d +0000\ndata %d\n%s\n", 1790000000+i, 1790000000+i, len(message), message)
	}
	repo.git(strings.NewReader(stream.String()), "fast-import", "--quiet")
	t.Chdir(repo.dir)
	f, api := commitsAPI(t)

	res := runCI(t, f, "", "commits", "--product", ciTestProduct, "--range", "HEAD")

	if res.err != nil {
		t.Fatalf("commits: %v", res.err)
	}
	calls := api.calls()
	if len(calls) != 2 {
		t.Fatalf("%d requests, want 2", len(calls))
	}
	for i, want := range []int{commitBatchSize, 1} {
		if got := len(calls[i].Body["commits"].([]any)); got != want {
			t.Fatalf("request %d carries %d commits, want %d", i, got, want)
		}
	}
	if res.stdout != "Zensu linked 1001 feature reference(s) from 1001 commit(s).\n" {
		t.Fatalf("stdout = %q", res.stdout)
	}

	f, _ = commitsAPI(t)
	res = runCI(t, f, "", "commits", "--json", "--product", ciTestProduct, "--range", "HEAD")
	var merged struct {
		Processed int               `json:"processed"`
		Links     []json.RawMessage `json:"links"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &merged); err != nil || merged.Processed != 1001 || len(merged.Links) != 1001 {
		t.Fatalf("--json merged %d processed and %d links (%v)", merged.Processed, len(merged.Links), err)
	}
}

func TestCICommits_RightTrimsMessages(t *testing.T) {
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	repo.commitAt("feat: keep the body\n\n  indented [ZEN-42] \t\n\n\n", "2026-10-01T09:00:00+02:00", "--cleanup=verbatim")
	t.Chdir(repo.dir)
	f, api := commitsAPI(t)
	if res := runCI(t, f, "", "commits", "--product", ciTestProduct); res.err != nil {
		t.Fatalf("commits: %v", res.err)
	}
	commits := api.only(t, http.MethodPost, "/api/ci/commits").Body["commits"].([]any)
	if got := commits[0].(map[string]any)["message"]; got != "feat: keep the body\n\n  indented [ZEN-42]" {
		t.Fatalf("message = %q", got)
	}
}

func TestCICommits_RefusesARangeThatLooksLikeAnOption(t *testing.T) {
	clearGitHubEnv(t)
	newCIHistory(t)
	planted := filepath.Join(t.TempDir(), "planted")
	for _, value := range []string{"-x", "--output=" + planted, ""} {
		f, api := commitsAPI(t)
		res := runCI(t, f, "", "commits", "--product", ciTestProduct, "--range="+value)
		want := fmt.Sprintf("--range %q starts with -; pass a revision range, such as main..HEAD", value)
		if value == "" {
			want = "--range needs a revision range, such as main..HEAD"
		}
		if res.err == nil || res.err.Error() != want {
			t.Errorf("--range=%s: error = %v", value, res.err)
		}
		if n := len(api.calls()); n != 0 {
			t.Errorf("--range=%s: %d requests", value, n)
		}
	}
	if _, err := os.Stat(planted); !os.IsNotExist(err) {
		t.Fatal("a range ran as a git option")
	}
}

func TestCICommits_RefusesAnUnknownRevisionBeforeAnyUpload(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	f, api := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--product", ciTestProduct, "--range", "nope.."+h.billing)
	if res.err == nil || !strings.HasPrefix(res.err.Error(), "git log: ") {
		t.Fatalf("error = %v, want git's refusal", res.err)
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}

func TestCICommits_LogEndsItsOptionsRightBeforeTheRevision(t *testing.T) {
	for _, tc := range []struct {
		selection commitSelection
		want      []string
	}{
		{commitSelection{revision: "main..HEAD"}, []string{"log", "-z", "--no-show-signature", "--format=" + commitLogFormat, "--end-of-options", "main..HEAD"}},
		{commitSelection{revision: "HEAD", single: true}, []string{"log", "-z", "--no-show-signature", "-1", "--format=" + commitLogFormat, "--end-of-options", "HEAD"}},
	} {
		if got := commitLogArgs(tc.selection); !slices.Equal(got, tc.want) {
			t.Errorf("commitLogArgs(%+v) = %q, want %q", tc.selection, got, tc.want)
		}
	}
}

func TestCICommits_RefusesALogRecordThatDoesNotStartWithACommitHash(t *testing.T) {
	record := "\x1fAda Lovelace\x1f2026-10-02T10:15:00+02:00\x1ffeat(auth): rotate refresh tokens [ZEN-42]\n\x00"
	if _, err := commitRecords([]byte("No signature\n" + strings.Repeat("a", 40) + record)); err == nil {
		t.Fatal("a record with text before its hash was accepted")
	}
	records, err := commitRecords([]byte(strings.Repeat("b", 64) + record))
	if err != nil || len(records) != 1 || records[0].Hash != strings.Repeat("b", 64) || records[0].Message != "feat(auth): rotate refresh tokens [ZEN-42]" {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
}

func TestCICommits_IgnoresSignatureOutputThatTheGitConfigurationTurnsOn(t *testing.T) {
	keygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("ssh-keygen is needed on the CI runner to sign a commit")
		}
		t.Skip("ssh-keygen is needed to sign a commit")
	}
	clearGitHubEnv(t)
	repo := newCIGitRepo(t)
	key := filepath.Join(t.TempDir(), "signing")
	if out, err := exec.Command(keygen, "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	repo.git(nil, "config", "gpg.format", "ssh")
	repo.git(nil, "config", "user.signingkey", key)
	repo.git(nil, "config", "log.showSignature", "true")
	signed := repo.commitAt("feat(auth): rotate refresh tokens [ZEN-42]", "2026-10-02T10:15:00+02:00", "-S")
	if !strings.Contains(repo.git(nil, "cat-file", "-p", signed), "gpgsig") {
		t.Fatal("the fixture commit is not signed")
	}
	t.Chdir(repo.dir)
	f, api := commitsAPI(t)

	res := runCI(t, f, "", "commits", "--product", ciTestProduct)

	if res.err != nil {
		t.Fatalf("commits: %v\n%s", res.err, res.stderr)
	}
	if got := sentHashes(t, api); !slices.Equal(got, []string{signed}) {
		t.Fatalf("sent %q, want %s", got, signed)
	}
}

type shallowMergeClone struct {
	base, head, merge string
}

func newShallowMergeClone(t *testing.T) shallowMergeClone {
	t.Helper()
	origin := newCIGitRepo(t)
	origin.commit("chore: initial import")
	origin.git(nil, "checkout", "-q", "-b", "feature")
	origin.commit("feat(auth): rotate refresh tokens [ZEN-42]")
	c := shallowMergeClone{head: origin.commit("feat(auth): expire old tokens [ZEN-7]")}
	origin.git(nil, "checkout", "-q", "main")
	c.base = origin.commit("chore: bump the build image")
	origin.git(nil, "merge", "-q", "--no-ff", "--no-edit", "feature")
	c.merge = origin.git(nil, "rev-parse", "HEAD")
	clone := filepath.Join(t.TempDir(), "clone")
	origin.git(nil, "clone", "-q", "--depth", "2", "file://"+origin.dir, clone)
	t.Chdir(clone)
	return c
}

func TestCICommits_WarnsWhenTheRangeReachesTheBoundaryOfAShallowCheckout(t *testing.T) {
	clearGitHubEnv(t)
	c := newShallowMergeClone(t)
	githubEvent(t, "pull_request", map[string]any{"pull_request": map[string]any{"base": map[string]any{"sha": c.base}, "head": map[string]any{"sha": c.head}}})
	t.Setenv("GITHUB_SHA", c.merge)
	f, api := commitsAPI(t)

	res := runCI(t, f, "", "commits", "--github", "--product", ciTestProduct)

	if res.err != nil {
		t.Fatalf("commits: %v\n%s", res.err, res.stderr)
	}
	if got := sentHashes(t, api); !slices.Equal(got, []string{c.head}) {
		t.Fatalf("sent %q, want only the head %s that the shallow checkout holds", got, c.head)
	}
	want := "::warning::This checkout is shallow, so " + c.base + ".." + c.head + " may miss the commits before " + c.head[:7] + ". Check out with fetch-depth: 0 to cover every commit.\n"
	if res.stderr != want {
		t.Fatalf("stderr = %q\nwant     %q", res.stderr, want)
	}
}

func TestCICommits_AFullHistoryGetsNoShallowWarning(t *testing.T) {
	clearGitHubEnv(t)
	h := newCIHistory(t)
	f, _ := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--product", ciTestProduct, "--range", h.base+".."+h.billing)
	if res.err != nil || res.stderr != "" {
		t.Fatalf("err = %v, stderr = %q", res.err, res.stderr)
	}
}

func TestCICommits_FailsOutsideAGitRepository(t *testing.T) {
	clearGitHubEnv(t)
	dir := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(dir))
	t.Chdir(dir)
	f, api := commitsAPI(t)
	res := runCI(t, f, "", "commits", "--product", ciTestProduct)
	if res.err == nil || !strings.HasPrefix(res.err.Error(), "git log: ") {
		t.Fatalf("error = %v", res.err)
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("%d requests", n)
	}
}
