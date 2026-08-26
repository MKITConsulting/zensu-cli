package testutil_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

type recordingTB struct {
	failed bool
	msg    string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatal(args ...any) {
	r.failed = true
	r.msg = fmt.Sprint(args...)
}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
}

func TestRunWithIsolatedConfigDir_RedirectsThenRestores(t *testing.T) {
	sentinel := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", sentinel)

	var seen string
	code := testutil.RunWithIsolatedConfigDir(func() int {
		dir, err := config.ConfigDir()
		if err != nil {
			t.Errorf("ConfigDir: %v", err)
			return 1
		}
		seen = dir
		return 0
	})

	if code != 0 {
		t.Fatalf("exit code: got %d want 0", code)
	}
	if seen == "" || seen == sentinel {
		t.Fatalf("config dir was not redirected away from %q: got %q", sentinel, seen)
	}
	if got, want := filepath.Dir(seen), filepath.Clean(os.TempDir()); got != want {
		t.Errorf("isolated dir %q lives in %q, want a child of %q", seen, got, want)
	}
	if _, err := os.Stat(seen); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("isolated dir %q survived the run: %v", seen, err)
	}
	if got := os.Getenv("ZENSU_CONFIG_DIR"); got != sentinel {
		t.Errorf("ZENSU_CONFIG_DIR after the run: got %q want the caller's %q", got, sentinel)
	}
}

func TestRunWithIsolatedConfigDir_PropagatesExitCode(t *testing.T) {
	if code := testutil.RunWithIsolatedConfigDir(func() int { return 7 }); code != 7 {
		t.Errorf("exit code: got %d want 7", code)
	}
}

func TestRequireIsolatedConfigDir_FailsClosedOutsideAnIsolatedRun(t *testing.T) {
	rec := &recordingTB{}
	if dir := testutil.RequireIsolatedConfigDir(rec); dir != "" {
		t.Errorf("guard returned %q outside an isolated run, want an empty path", dir)
	}
	if !rec.failed {
		t.Fatal("guard did not fail outside an isolated run — tests could write the real credential store")
	}
}
