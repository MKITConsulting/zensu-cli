package auth_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
)

func TestDiscoverEndpoints_UsesWellKnownWhenAvailable(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-authorization-server" {
			t.Errorf("unexpected discovery path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": srv.URL + "/authz/authorize",
			"token_endpoint":         srv.URL + "/authz/token",
		})
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL, nil)
	if ep.Authorization != srv.URL+"/authz/authorize" {
		t.Errorf("a same-host discovery document must win over the conventional fallback path; got %q", ep.Authorization)
	}
	if ep.Token != srv.URL+"/authz/token" {
		t.Errorf("a same-host discovery document must win over the conventional fallback path; got %q", ep.Token)
	}
}

func TestDiscoverEndpoints_FallsBackOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL, nil)
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
			"access_token": "acc-1", "refresh_token": "ref-1", "expires_in": 900, "token_type": "Bearer", "scope": "read write",
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
	if tok.Scope != "read write" {
		t.Errorf("scope echo: got %q want %q", tok.Scope, "read write")
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

func TestPostToken_KeepsControlBytesOfAnOAuthErrorOffTheTerminal(t *testing.T) {
	cases := []struct {
		name string
		body map[string]string
		want string
	}{
		{"description", map[string]string{"error": "invalid_grant", "error_description": "\x1b]0;pwned\x07x"}, "token endpoint: invalid_grant: ]0;pwnedx"},
		{"code and description", map[string]string{"error": "invalid_grant\x1b[2J", "error_description": "expired \xe2\x80\xaegnp.exe\xc2\x9b"}, "token endpoint: invalid_grant[2J: expired gnp.exe"},
		{"code of control bytes only", map[string]string{"error": "\x1b\x07", "error_description": "x"}, "token endpoint returned 400"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(tc.body)
			}))
			defer srv.Close()
			if _, err := auth.ExchangeCode(context.Background(), srv.Client(), srv.URL, "http://127.0.0.1/callback", "x", "y"); err == nil || err.Error() != tc.want {
				t.Errorf("ExchangeCode error = %q, want %q", err, tc.want)
			}
			if _, err := auth.RefreshToken(context.Background(), srv.Client(), srv.URL, "r1"); err == nil || err.Error() != tc.want {
				t.Errorf("RefreshToken error = %q, want %q", err, tc.want)
			}
		})
	}
}

func TestPostToken_ReportsRequestTransportAndDecodeFailures(t *testing.T) {
	if _, err := auth.RefreshToken(context.Background(), http.DefaultClient, "http://zensu.test/\x7f", "r1"); err == nil || !strings.Contains(err.Error(), "invalid control character in URL") {
		t.Errorf("unusable token endpoint error = %v", err)
	}
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closed.URL
	closed.Close()
	if _, err := auth.RefreshToken(context.Background(), http.DefaultClient, closedURL, "r1"); err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("refused connection error = %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "not json")
	}))
	defer srv.Close()
	if _, err := auth.RefreshToken(context.Background(), srv.Client(), srv.URL, "r1"); err == nil || !strings.HasPrefix(err.Error(), "decoding token response: ") {
		t.Errorf("undecodable token response error = %v", err)
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
		switch r.Header.Get("X-API-Key") {
		case "zsk_good":
			w.WriteHeader(http.StatusOK)
		case "zsk_agent":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"agent_key_not_allowed","message":"an agent key serves only the work order worker routes"}`))
		case "zsk_forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"insufficient_scope","message":"no"}`))
		case "zsk_garbled":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`not json`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	cases := []struct {
		key     string
		want    auth.APIKeyKind
		wantErr string
	}{
		{"zsk_good", auth.APIKeyKindUser, ""},
		{"zsk_agent", auth.APIKeyKindAgent, ""},
		{"zsk_bad", "", "api key rejected (status 401)"},
		{"zsk_forbidden", "", "api key rejected (status 403)"},
		{"zsk_garbled", "", "api key rejected (status 403)"},
	}
	for _, tc := range cases {
		kind, err := auth.ValidateAPIKey(context.Background(), srv.Client(), srv.URL, tc.key)
		if tc.wantErr == "" && (err != nil || kind != tc.want) {
			t.Errorf("%s: kind %q err %v, want %q", tc.key, kind, err, tc.want)
		}
		if tc.wantErr != "" && (err == nil || err.Error() != tc.wantErr || kind != "") {
			t.Errorf("%s: kind %q err %v, want %q", tc.key, kind, err, tc.wantErr)
		}
	}
}

func TestValidateAPIKey_TransportAndRequestErrors(t *testing.T) {
	if _, err := auth.ValidateAPIKey(context.Background(), http.DefaultClient, "http://127.0.0.1:1", "zsk_x"); err == nil {
		t.Error("a refused connection must fail")
	}
	if _, err := auth.ValidateAPIKey(context.Background(), http.DefaultClient, "http://bad host", "zsk_x"); err == nil {
		t.Error("an invalid URL must fail")
	}
}
