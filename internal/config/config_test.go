package config_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestLoad_MissingFileReturnsEmptyConfig(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load() on missing file should not error, got %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
	if cfg.AccessToken != "" || cfg.APIKey != "" || cfg.APIURL != "" {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

func TestSaveThenLoad_RoundTrips(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())

	exp := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	in := &config.Config{
		APIURL:       "https://api.example.test",
		AccessToken:  "acc-123",
		RefreshToken: "ref-456",
		ExpiresAt:    exp,
		APIKey:       "",
		User:         "dev@example.test",
		Org:          "Acme",
	}
	if err := in.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	out, err := config.Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if out.APIURL != in.APIURL {
		t.Errorf("APIURL: got %q want %q", out.APIURL, in.APIURL)
	}
	if out.AccessToken != in.AccessToken {
		t.Errorf("AccessToken: got %q want %q", out.AccessToken, in.AccessToken)
	}
	if out.RefreshToken != in.RefreshToken {
		t.Errorf("RefreshToken: got %q want %q", out.RefreshToken, in.RefreshToken)
	}
	if !out.ExpiresAt.Equal(in.ExpiresAt) {
		t.Errorf("ExpiresAt: got %v want %v", out.ExpiresAt, in.ExpiresAt)
	}
	if out.User != in.User {
		t.Errorf("User: got %q want %q", out.User, in.User)
	}
	if out.Org != in.Org {
		t.Errorf("Org: got %q want %q", out.Org, in.Org)
	}
}

func TestSave_FilePermsAre0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix file permissions not applicable on windows")
	}
	dir := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", dir)

	cfg := &config.Config{APIURL: "https://api.example.test", APIKey: "zsk_secret"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "hosts.json"))
	if err != nil {
		t.Fatalf("stat hosts.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("hosts.json perms: got %o want 600", perm)
	}
}

func TestConfigDir_ZensuConfigDirOverride(t *testing.T) {
	want := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", want)

	got, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() error: %v", err)
	}
	if got != want {
		t.Errorf("ConfigDir(): got %q want %q", got, want)
	}
}

func TestConfigDir_XDGConfigHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("XDG_CONFIG_HOME is unix-only")
	}
	t.Setenv("ZENSU_CONFIG_DIR", "")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	got, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir() error: %v", err)
	}
	if want := filepath.Join(xdg, "zensu"); got != want {
		t.Errorf("ConfigDir(): got %q want %q", got, want)
	}
}

func TestResolveAPIURL_Precedence(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		flag   string
		env    string
		want   string
	}{
		{"flag wins over all", "https://stored", "https://flag", "https://env", "https://flag"},
		{"env when no flag", "https://stored", "", "https://env", "https://env"},
		{"stored when no flag/env", "https://stored", "", "", "https://stored"},
		{"default when none", "", "", "", config.DefaultAPIURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{APIURL: tc.stored}
			if got := cfg.ResolveAPIURL(tc.flag, tc.env); got != tc.want {
				t.Errorf("ResolveAPIURL(%q,%q) with stored %q: got %q want %q", tc.flag, tc.env, tc.stored, got, tc.want)
			}
		})
	}
}
