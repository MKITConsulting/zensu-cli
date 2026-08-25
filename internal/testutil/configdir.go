package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

type TB interface {
	Helper()
	Fatalf(format string, args ...any)
}

var isolatedDir string

func RunWithIsolatedConfigDir(run func() int) int {
	dir, err := os.MkdirTemp("", "zensu-config-isolation-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: creating isolated config dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	if err := os.Setenv("ZENSU_CONFIG_DIR", dir); err != nil {
		fmt.Fprintf(os.Stderr, "testutil: setting ZENSU_CONFIG_DIR: %v\n", err)
		return 1
	}
	resolved, err := config.ConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: resolving config dir: %v\n", err)
		return 1
	}
	if filepath.Clean(resolved) != filepath.Clean(dir) {
		fmt.Fprintf(os.Stderr, "testutil: config dir resolved to %s, want the isolated dir %s\n", resolved, dir)
		return 1
	}
	isolatedDir = dir

	return run()
}

func RequireIsolatedConfigDir(t TB) string {
	t.Helper()

	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("config.ConfigDir: %v", err)
	}
	if real, realErr := config.UserConfigDir(); realErr == nil && filepath.Clean(dir) == filepath.Clean(real) {
		t.Fatalf("config.ConfigDir() resolves to the real zensu config dir %q — this package's TestMain must call testutil.RunWithIsolatedConfigDir", dir)
	}
	if !UnderTempDir(dir) {
		t.Fatalf("config.ConfigDir() resolves to %q, which is not under the temp dir %q", dir, os.TempDir())
	}
	if isolatedDir == "" {
		t.Fatalf("config.ConfigDir() is %q but no isolated dir was created — this package's TestMain must call testutil.RunWithIsolatedConfigDir", dir)
	}
	if filepath.Clean(dir) != filepath.Clean(isolatedDir) {
		t.Fatalf("config.ConfigDir() is %q but TestMain isolated %q", dir, isolatedDir)
	}
	return dir
}

func UnderTempDir(dir string) bool {
	tmp := filepath.Clean(os.TempDir())
	return strings.HasPrefix(filepath.Clean(dir), tmp+string(os.PathSeparator))
}
