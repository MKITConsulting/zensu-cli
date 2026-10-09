package testutil

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const envConfigDir = "ZENSU_CONFIG_DIR"

type TB interface {
	Helper()
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

var isolatedDir string

func RunWithIsolatedConfigDir(run func() int) int {
	dir, err := os.MkdirTemp("", "zensu-config-isolation-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: creating isolated config dir: %v\n", err)
		return 1
	}

	previous, had := os.LookupEnv(envConfigDir)
	if err := os.Setenv(envConfigDir, dir); err != nil {
		fmt.Fprintf(os.Stderr, "testutil: setting %s: %v\n", envConfigDir, err)
		_ = os.RemoveAll(dir)
		return 1
	}

	resolved, err := config.ConfigDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testutil: resolving config dir: %v\n", err)
		restoreConfigDirEnv(previous, had)
		_ = os.RemoveAll(dir)
		return 1
	}
	if filepath.Clean(resolved) != filepath.Clean(dir) {
		fmt.Fprintf(os.Stderr, "testutil: config dir resolved to %s, want the isolated dir %s\n", resolved, dir)
		restoreConfigDirEnv(previous, had)
		_ = os.RemoveAll(dir)
		return 1
	}
	isolatedDir = dir

	aborted := make(chan os.Signal, 1)
	signal.Notify(aborted, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-aborted
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}()

	code := run()

	signal.Stop(aborted)
	isolatedDir = ""
	restoreConfigDirEnv(previous, had)
	_ = os.RemoveAll(dir)
	return code
}

func restoreConfigDirEnv(previous string, had bool) {
	if had {
		_ = os.Setenv(envConfigDir, previous)
		return
	}
	_ = os.Unsetenv(envConfigDir)
}

func RequireIsolatedConfigDir(t TB) string {
	t.Helper()

	dir, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("config.ConfigDir: %v", err)
		return ""
	}
	if real, realErr := config.UserConfigDir(); realErr == nil && filepath.Clean(dir) == filepath.Clean(real) {
		t.Fatalf("config.ConfigDir() resolves to the real zensu config dir %q — this package's TestMain must call testutil.RunWithIsolatedConfigDir", dir)
		return ""
	}
	if !UnderTempDir(dir) {
		t.Fatalf("config.ConfigDir() resolves to %q, which is not under the temp dir %q", dir, os.TempDir())
		return ""
	}
	if isolatedDir == "" {
		t.Fatalf("config.ConfigDir() is %q but no isolated dir was created — this package's TestMain must call testutil.RunWithIsolatedConfigDir", dir)
		return ""
	}
	if filepath.Clean(dir) != filepath.Clean(isolatedDir) {
		t.Fatalf("config.ConfigDir() is %q but TestMain isolated %q", dir, isolatedDir)
		return ""
	}
	return dir
}

func UnderTempDir(dir string) bool {
	tmp := filepath.Clean(os.TempDir())
	return strings.HasPrefix(filepath.Clean(dir), tmp+string(os.PathSeparator))
}

func ClearIsolatedConfigDir(t TB, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading isolated config dir: %v", err)
		return
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			t.Fatalf("clearing %s: %v", entry.Name(), err)
			return
		}
	}
}

func SoleEntry(t TB, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading isolated config dir: %v", err)
		return ""
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("isolated config dir holds %d entries %v, want exactly one written file", len(entries), names)
		return ""
	}
	return filepath.Join(dir, entries[0].Name())
}

func CredentialStoreEntry(t TB, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading isolated config dir: %v", err)
		return ""
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if len(names) != 2 || names[0] != "hosts.json" || names[1] != "hosts.json.lock" {
		t.Fatalf("isolated config dir holds %v, want exactly the credential store hosts.json and its lock file hosts.json.lock", names)
		return ""
	}
	return filepath.Join(dir, "hosts.json")
}
