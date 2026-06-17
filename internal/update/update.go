package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	repo          = "MKITConsulting/zensu-cli"
	cacheFileName = "version-check.json"
	checkInterval = 24 * time.Hour
	fetchTimeout  = 2 * time.Second
	maxResponse   = 1 << 20
	cacheDirPerm  = 0o700
	cacheFilePerm = 0o600
)

var githubAPIBase = "https://api.github.com"

var graceWait = 300 * time.Millisecond

var osArgs = func() []string { return os.Args[1:] }

var semverPattern = regexp.MustCompile(`^v?\d{1,9}(\.\d{1,9}){0,2}([-+][0-9A-Za-z.-]+)?$`)

func validVersion(v string) bool { return semverPattern.MatchString(strings.TrimSpace(v)) }

type cache struct {
	CheckedAt     time.Time `json:"checkedAt"`
	LatestVersion string    `json:"latestVersion"`
}

func Start(current string) (<-chan string, context.CancelFunc) {
	ch := make(chan string, 1)
	if suppressed(current, osArgs()) {
		ch <- ""
		return ch, func() {}
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	go func() {
		defer cancel()
		ch <- Notice(ctx, current, time.Now())
	}()
	return ch, cancel
}

func Finish(ch <-chan string, w io.Writer) {
	select {
	case notice := <-ch:
		if notice != "" {
			fmt.Fprintln(w, notice)
		}
	case <-time.After(graceWait):
	}
}

func Notice(ctx context.Context, current string, now time.Time) string {
	latest := ""
	if c, err := loadCache(); err == nil && c.LatestVersion != "" && now.Sub(c.CheckedAt) < checkInterval {
		latest = c.LatestVersion
	} else {
		fetched, err := fetchLatest(ctx, current)
		if err != nil || fetched == "" {
			return ""
		}
		latest = fetched
		_ = saveCache(cache{CheckedAt: now, LatestVersion: latest})
	}
	if !validVersion(latest) || compareSemver(latest, current) <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"update available: zensu %s (you have %s) — curl -fsSL https://zensu.dev/install.sh | sh",
		normalize(latest), strings.TrimPrefix(normalize(current), "v"),
	)
}

func suppressed(current string, args []string) bool {
	return envSuppressed(current, args) || !isTerminalStderr()
}

var isTerminalStderr = func() bool { return isTerminal(os.Stderr) }

func envSuppressed(current string, args []string) bool {
	if current == "" || current == "dev" {
		return true
	}
	for _, env := range []string{"ZENSU_NO_UPDATE_CHECK", "CI", "NO_UPDATE_NOTIFIER"} {
		if os.Getenv(env) != "" {
			return true
		}
	}
	for _, a := range args {
		if a == "completion" || a == "__complete" || a == "__completeNoDesc" {
			return true
		}
	}
	return false
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func cachePath() (string, error) {
	dir, err := config.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, cacheFileName), nil
}

func loadCache() (cache, error) {
	var c cache
	path, err := cachePath()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	err = json.Unmarshal(data, &c)
	return c, err
}

func saveCache(c cache) error {
	dir, err := config.ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, cacheDirPerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, cacheFileName+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(cacheFilePerm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, cacheFileName))
}

func fetchLatest(ctx context.Context, current string) (string, error) {
	url := strings.TrimRight(githubAPIBase, "/") + "/repos/" + repo + "/releases/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "zensu-cli/"+current)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github releases API: status %d", resp.StatusCode)
	}
	var meta struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&meta); err != nil {
		return "", err
	}
	tag := strings.TrimSpace(meta.TagName)
	if !validVersion(tag) {
		return "", fmt.Errorf("github releases API: invalid tag %q", tag)
	}
	return tag, nil
}

func normalize(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

func compareSemver(a, b string) int {
	av, bv := parseVersion(a), parseVersion(b)
	for i := 0; i < 3; i++ {
		switch {
		case av[i] < bv[i]:
			return -1
		case av[i] > bv[i]:
			return 1
		}
	}
	return 0
}

func parseVersion(v string) [3]int {
	v = strings.TrimPrefix(normalize(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, part := range strings.Split(v, ".") {
		if i >= 3 {
			break
		}
		if n, err := strconv.Atoi(part); err == nil {
			out[i] = n
		}
	}
	return out
}
