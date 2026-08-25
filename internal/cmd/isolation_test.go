package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithIsolatedConfigDir(m.Run))
}

func TestConfigDir_IsIsolatedFromRealCredentialStore(t *testing.T) {
	resolved, err := config.ConfigDir()
	if err != nil {
		t.Fatalf("config.ConfigDir: %v", err)
	}
	real, err := config.UserConfigDir()
	if err != nil {
		t.Fatalf("config.UserConfigDir: %v", err)
	}
	if filepath.Clean(resolved) == filepath.Clean(real) {
		t.Fatalf("config.ConfigDir() resolves to the real credential store %q — every test in this package would overwrite it", resolved)
	}
	if !testutil.UnderTempDir(resolved) {
		t.Fatalf("config.ConfigDir() resolves to %q, which is not under the temp dir %q", resolved, os.TempDir())
	}
	if isolated := testutil.RequireIsolatedConfigDir(t); filepath.Clean(isolated) != filepath.Clean(resolved) {
		t.Fatalf("config.ConfigDir() is %q but TestMain isolated %q", resolved, isolated)
	}
}

func TestDefaultSaver_WritesIntoIsolatedConfigDir(t *testing.T) {
	dir := testutil.RequireIsolatedConfigDir(t)
	hosts := filepath.Join(dir, "hosts.json")
	if err := os.Remove(hosts); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clearing %s: %v", hosts, err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "isolated-access",
				"refresh_token": "isolated-refresh",
				"expires_in":    900,
			})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))

	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if cfg.AccessToken != "isolated-access" {
		t.Fatalf("refresh did not run: in-memory access token is %q", cfg.AccessToken)
	}

	raw, err := os.ReadFile(hosts)
	if err != nil {
		t.Fatalf("the default saver did not write %s: %v", hosts, err)
	}
	var persisted config.Config
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decoding %s: %v", hosts, err)
	}
	if persisted.AccessToken != "isolated-access" {
		t.Errorf("persisted access token: got %q want %q", persisted.AccessToken, "isolated-access")
	}
	if persisted.RefreshToken != "isolated-refresh" {
		t.Errorf("persisted refresh token: got %q want %q", persisted.RefreshToken, "isolated-refresh")
	}
}
