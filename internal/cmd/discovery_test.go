package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestNewClient_DiscoversTheTokenEndpointOnlyForARefresh(t *testing.T) {
	t.Setenv(sessionTokenEnv, "")
	t.Setenv("ZENSU_API_URL", "")
	t.Setenv(trustedAuthHostsEnv, "")

	var mu sync.Mutex
	hits := map[string]int{}
	var origin string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]string{
				"authorization_endpoint": origin + "/oauth/authorize",
				"token_endpoint":         origin + "/oauth/token",
			})
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "refresh_token": "r2", "expires_in": 900})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()
	origin = srv.URL
	count := func(path string) int {
		mu.Lock()
		defer mu.Unlock()
		return hits[path]
	}
	get := func() {
		t.Helper()
		c, err := newClient(context.Background(), "")
		if err != nil {
			t.Fatalf("newClient: %v", err)
		}
		resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close()
	}

	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "valid", RefreshToken: "r1", ExpiresAt: time.Now().Add(time.Hour)})
	get()
	if n := count("/.well-known/oauth-authorization-server"); n != 0 {
		t.Fatalf("a request with a valid token ran endpoint discovery %d times, want none", n)
	}

	storeLogin(t, config.Config{APIURL: srv.URL, AccessToken: "expired", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Hour)})
	get()
	if n := count("/.well-known/oauth-authorization-server"); n != 1 {
		t.Errorf("a refresh ran endpoint discovery %d times, want once", n)
	}
	if n := count("/oauth/token"); n != 1 {
		t.Errorf("token endpoint called %d times, want once", n)
	}
}
