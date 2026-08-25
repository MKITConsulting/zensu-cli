package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithIsolatedConfigDir(m.Run))
}

func TestConfigDir_IsIsolatedFromRealCredentialStore(t *testing.T) {
	testutil.RequireIsolatedConfigDir(t)
}

func TestSave_RefusesTheRealCredentialStoreUnderTest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AppData", home)

	pretendReal, err := config.UserConfigDir()
	if err != nil {
		t.Fatalf("config.UserConfigDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(pretendReal), filepath.Clean(home)) {
		t.Fatalf("home redirection did not take: UserConfigDir is %q, not under %q — refusing to run this test against the real store", pretendReal, home)
	}
	t.Setenv("ZENSU_CONFIG_DIR", pretendReal)

	cfg := &config.Config{AccessToken: "must-not-be-written", RefreshToken: "must-not-be-written"}
	err = cfg.Save()
	if err == nil {
		t.Fatal("Save wrote the user config dir from a test binary; the guard is gone")
	}
	if !strings.Contains(err.Error(), "refusing to write the real zensu config dir") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(pretendReal, "hosts.json")); !os.IsNotExist(statErr) {
		t.Fatalf("Save left a file behind: stat error is %v", statErr)
	}
}

func TestSave_AllowsAnIsolatedConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", dir)

	cfg := &config.Config{AccessToken: "isolated"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save into an isolated dir must succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "hosts.json")); err != nil {
		t.Fatalf("stat hosts.json: %v", err)
	}
}
