package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
)

func TestDiscoverEndpoints_UsesWellKnownWhenAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			t.Errorf("unexpected discovery path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "https://issuer.test/oauth/authorize",
			"token_endpoint":         "https://issuer.test/oauth/token",
		})
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
	if ep.Authorization != "https://issuer.test/oauth/authorize" {
		t.Errorf("Authorization: got %q", ep.Authorization)
	}
	if ep.Token != "https://issuer.test/oauth/token" {
		t.Errorf("Token: got %q", ep.Token)
	}
}

func TestDiscoverEndpoints_FallsBackOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
	if ep.Authorization != srv.URL+"/oauth/authorize" {
		t.Errorf("fallback Authorization: got %q want %q", ep.Authorization, srv.URL+"/oauth/authorize")
	}
	if ep.Token != srv.URL+"/oauth/token" {
		t.Errorf("fallback Token: got %q want %q", ep.Token, srv.URL+"/oauth/token")
	}
}

func TestAuthorizeURL_ContainsPKCEParams(t *testing.T) {
	raw := auth.AuthorizeURL("https://issuer.test/oauth/authorize", "http://127.0.0.1:5000/callback", "chal-xyz", "state-abc", "mcp:read mcp:write")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := u.Query()
	checks := map[string]string{
		"response_type":         "code",
		"client_id":             auth.ClientID,
		"redirect_uri":          "http://127.0.0.1:5000/callback",
		"code_challenge":        "chal-xyz",
		"code_challenge_method": "S256",
		"state":                 "state-abc",
		"scope":                 "mcp:read mcp:write",
	}
	for k, want := range checks {
		if got := q.Get(k); got != want {
			t.Errorf("query %s: got %q want %q", k, got, want)
		}
	}
	if u.Scheme != "https" || u.Host != "issuer.test" || u.Path != "/oauth/authorize" {
		t.Errorf("base URL wrong: %s", raw)
	}
}

func TestExchangeCode_PostsFormAndParsesTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s want POST", r.Method)
		}
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("grant_type: got %q", r.Form.Get("grant_type"))
		}
		if r.Form.Get("code") != "auth-code-1" {
			t.Errorf("code: got %q", r.Form.Get("code"))
		}
		if r.Form.Get("code_verifier") != "verifier-1" {
			t.Errorf("code_verifier: got %q", r.Form.Get("code_verifier"))
		}
		if r.Form.Get("client_id") != auth.ClientID {
			t.Errorf("client_id: got %q", r.Form.Get("client_id"))
		}
		if r.Form.Get("redirect_uri") != "http://127.0.0.1:5000/callback" {
			t.Errorf("redirect_uri: got %q", r.Form.Get("redirect_uri"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "acc-1", "refresh_token": "ref-1", "expires_in": 900, "token_type": "Bearer",
		})
	}))
	defer srv.Close()

	tok, err := auth.ExchangeCode(context.Background(), srv.Client(), srv.URL, "http://127.0.0.1:5000/callback", "auth-code-1", "verifier-1")
	if err != nil {
		t.Fatalf("ExchangeCode error: %v", err)
	}
	if tok.AccessToken != "acc-1" || tok.RefreshToken != "ref-1" || tok.ExpiresIn != 900 {
		t.Errorf("token mismatch: %+v", tok)
	}
}

func TestExchangeCode_ErrorsOnOAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "bad code"})
	}))
	defer srv.Close()

	_, err := auth.ExchangeCode(context.Background(), srv.Client(), srv.URL, "http://127.0.0.1/callback", "x", "y")
	if err == nil {
		t.Fatal("expected error on invalid_grant, got nil")
	}
}

func TestRefreshToken_PostsRefreshGrant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			t.Errorf("grant_type: got %q want refresh_token", r.Form.Get("grant_type"))
		}
		if r.Form.Get("refresh_token") != "old-refresh" {
			t.Errorf("refresh_token: got %q", r.Form.Get("refresh_token"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "acc-2", "refresh_token": "ref-2", "expires_in": 900,
		})
	}))
	defer srv.Close()

	tok, err := auth.RefreshToken(context.Background(), srv.Client(), srv.URL, "old-refresh")
	if err != nil {
		t.Fatalf("RefreshToken error: %v", err)
	}
	if tok.AccessToken != "acc-2" || tok.RefreshToken != "ref-2" {
		t.Errorf("token mismatch: %+v", tok)
	}
}

func TestValidateAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "zsk_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := auth.ValidateAPIKey(context.Background(), srv.Client(), srv.URL, "zsk_good"); err != nil {
		t.Errorf("valid key should pass, got %v", err)
	}
	if err := auth.ValidateAPIKey(context.Background(), srv.Client(), srv.URL, "zsk_bad"); err == nil {
		t.Error("invalid key should error, got nil")
	}
}
