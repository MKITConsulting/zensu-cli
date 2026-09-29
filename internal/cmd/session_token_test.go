package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

func storeLogin(t *testing.T, cfg config.Config) {
	t.Helper()
	dir := testutil.RequireIsolatedConfigDir(t)
	t.Cleanup(func() { testutil.ClearIsolatedConfigDir(t, dir) })
	if err := cfg.Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
}

func TestNewClient_SessionTokenOverridesStoredLogin(t *testing.T) {
	var mu sync.Mutex
	var paths, auths, keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		auths = append(auths, r.Header.Get("Authorization"))
		keys = append(keys, r.Header.Get("X-API-Key"))
		mu.Unlock()
		if r.URL.Path == "/api/work-orders/o1/heartbeat" {
			_, _ = w.Write([]byte(`{"order":{"id":"o1","status":"implementing"},"lease_seconds":1800,"answers":[]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	storeLogin(t, config.Config{APIURL: srv.URL, APIKey: "zsk_stored_key", AccessToken: "stored-access", RefreshToken: "stored-refresh"})
	t.Setenv(sessionTokenEnv, "zst_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	t.Setenv("ZENSU_API_URL", "")

	c, err := newClient(context.Background(), "")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	resp, err := c.Do(context.Background(), http.MethodPost, "/api/work-orders/o1/heartbeat", nil)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != 1 || paths[0] != "/api/work-orders/o1/heartbeat" {
		t.Fatalf("requests = %v, want only the heartbeat (no endpoint discovery)", paths)
	}
	if auths[0] != "Bearer zst_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG" || keys[0] != "" {
		t.Fatalf("authorization = %q, api key = %q; the session token must replace the stored login", auths[0], keys[0])
	}
}

func TestNewClient_SessionTokenNeverRefreshesOn401(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"invalid_session_token","message":"session token expired or revoked"}`))
	}))
	defer srv.Close()
	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "stored-access", RefreshToken: "stored-refresh"})
	t.Setenv(sessionTokenEnv, "zst_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	t.Setenv("ZENSU_API_URL", "")

	f := &Factory{Out: &strings.Builder{}, NewClient: func(ctx context.Context) (*client.Client, error) { return newClient(ctx, "") }}
	_, err := f.request(context.Background(), http.MethodPost, "/api/work-orders/o1/heartbeat", nil)
	if err == nil || !strings.Contains(err.Error(), "session token expired or revoked") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want the 401 surfaced", err)
	}
	if calls != 1 {
		t.Fatalf("server calls = %d, want 1 (no refresh, no retry)", calls)
	}
}

func TestNewClient_SessionTokenNeedsItsPrefix(t *testing.T) {
	t.Setenv(sessionTokenEnv, "zsk_not_a_session_token")
	_, err := newClient(context.Background(), "http://127.0.0.1:1")
	if err == nil || !strings.Contains(err.Error(), "ZENSU_SESSION_TOKEN must hold a work order session token") {
		t.Fatalf("error = %v, want the prefix refusal", err)
	}
}

func TestNewClient_SessionTokenHonorsTheAPIURLFlag(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	storeLogin(t, config.Config{APIURL: "http://127.0.0.1:1"})
	t.Setenv(sessionTokenEnv, "zst_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG")
	c, err := newClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/work-orders/o1", nil)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if hits != 1 || c.BaseURL != srv.URL {
		t.Fatalf("hits = %d base = %s, want the flag's host", hits, c.BaseURL)
	}
}

func TestNewClient_SessionTokenResolvesItsHostFromEachSource(t *testing.T) {
	for _, tc := range []struct {
		name, flag, env, stored, want string
	}{
		{"flag", "https://flag.example.test", "", "", "https://flag.example.test"},
		{"environment", "", "https://env.example.test", "", "https://env.example.test"},
		{"stored host", "", "", "https://stored.example.test", "https://stored.example.test"},
		{"flag before environment and stored host", "https://flag.example.test", "https://env.example.test", "https://stored.example.test", "https://flag.example.test"},
		{"environment before stored host", "", "https://env.example.test", "https://stored.example.test", "https://env.example.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storeLogin(t, config.Config{APIURL: tc.stored, APIKey: "zsk_stored_key"})
			t.Setenv(sessionTokenEnv, woSessionToken)
			t.Setenv("ZENSU_API_URL", tc.env)
			c, err := newClient(context.Background(), tc.flag)
			if err != nil {
				t.Fatalf("newClient: %v", err)
			}
			if c.BaseURL != tc.want || c.AuthMode() != client.AuthModeSessionToken {
				t.Fatalf("base = %q mode = %q, want %q with the session token", c.BaseURL, c.AuthMode(), tc.want)
			}
		})
	}
}

func TestNewClient_SessionTokenRefusesTheBuiltInDefaultHost(t *testing.T) {
	storeLogin(t, config.Config{APIKey: "zsk_stored_key"})
	t.Setenv(sessionTokenEnv, woSessionToken)
	t.Setenv("ZENSU_API_URL", "")
	c, err := newClient(context.Background(), "")
	if c != nil || err == nil || err.Error() != "ZENSU_SESSION_TOKEN needs the host that issued it: set ZENSU_API_URL or pass --api-url; a session token is never sent to the built-in default https://api.zensu.dev" {
		t.Fatalf("client = %v, error = %v, want the refusal", c, err)
	}
}

func TestNewClient_SurfacesAnUnreadableConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "hosts.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv(sessionTokenEnv, woSessionToken)
	c, err := newClient(context.Background(), "https://flag.example.test")
	if c != nil || err == nil || err.Error() != "unexpected end of JSON input" {
		t.Fatalf("client = %v, error = %v, want the config error", c, err)
	}
}

func TestNewClient_WithoutSessionTokenUsesStoredAPIKey(t *testing.T) {
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/work-orders" {
			key = r.Header.Get("X-API-Key")
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	storeLogin(t, config.Config{APIURL: srv.URL, APIKey: "zsk_stored_key"})
	t.Setenv(sessionTokenEnv, "")
	t.Setenv("ZENSU_API_URL", "")
	c, err := newClient(context.Background(), "")
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/work-orders", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	resp.Body.Close()
	if key != "zsk_stored_key" {
		t.Fatalf("X-API-Key = %q, want the stored key", key)
	}
}
