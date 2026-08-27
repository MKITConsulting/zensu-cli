package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
)

func TestDiscoverEndpoints_RejectsSchemeDowngrade(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "http://attacker.example/oauth/authorize",
			"token_endpoint":         "http://attacker.example/oauth/token",
		})
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL, nil)
	if ep.Token != srv.URL+"/oauth/token" {
		t.Errorf("downgraded http token_endpoint must be rejected → fallback; got %q", ep.Token)
	}
	if ep.Authorization != srv.URL+"/oauth/authorize" {
		t.Errorf("downgraded http authorization_endpoint must be rejected → fallback; got %q", ep.Authorization)
	}
}

func TestDiscoverEndpoints_RejectsCrossHostTokenEndpointByDefault(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "https://mcp.other.example/oauth/authorize",
			"token_endpoint":         "https://mcp.other.example/oauth/token",
		})
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL, nil)
	if ep.Token != srv.URL+"/oauth/token" {
		t.Errorf("the refresh token is sent to the token endpoint, so a host the user never named must not be honored without opting in; got %q", ep.Token)
	}
	if ep.Authorization != srv.URL+"/oauth/authorize" {
		t.Errorf("a cross-host authorization_endpoint must fall back for the same reason; got %q", ep.Authorization)
	}
}

func TestDiscoverEndpoints_AllowsCrossHostWhenTheHostIsExplicitlyTrusted(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "https://mcp.other.example/oauth/authorize",
			"token_endpoint":         "https://mcp.other.example/oauth/token",
		})
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL, []string{"MCP.Other.Example"})
	if ep.Token != "https://mcp.other.example/oauth/token" {
		t.Errorf("a separate auth host stays possible when the operator names it, and the match is case-insensitive; got %q", ep.Token)
	}
}

func TestDiscoverEndpoints_TreatsASpelledOutDefaultPortAsTheSameHost(t *testing.T) {
	if !auth.EndpointTrusted("https://api.example", "https://api.example:443/oauth/token", nil) {
		t.Error("https://api.example and https://api.example:443 are the same origin, so spelling the port out must not force a fallback")
	}
	if auth.EndpointTrusted("https://api.example", "https://api.example:8443/oauth/token", nil) {
		t.Error("a real port change is a different origin and must not be trusted")
	}
	if !auth.EndpointTrusted("https://API.Example", "https://api.example/oauth/token", nil) {
		t.Error("host comparison must be case-insensitive")
	}
}

func TestDiscoverEndpoints_RejectsATrustedHostThatDowngradesTheScheme(t *testing.T) {
	if auth.EndpointTrusted("https://api.example", "http://mcp.other.example/oauth/token", []string{"mcp.other.example"}) {
		t.Error("naming a host as trusted must not also waive the https requirement")
	}
}

func TestDiscoverEndpoints_NormalizesThePlainHTTPDefaultPort(t *testing.T) {
	if !auth.EndpointTrusted("http://api.internal", "http://api.internal:80/oauth/token", nil) {
		t.Error("a self-hosted plaintext deployment must still recognise its own host when the default port is spelled out")
	}
	if auth.EndpointTrusted("http://api.internal", "http://api.internal:8080/oauth/token", nil) {
		t.Error("a different port is a different origin over http too")
	}
}

func TestDiscoverEndpoints_RefusesUnparseableURLs(t *testing.T) {
	if auth.EndpointTrusted("://not-a-url", "https://api.example/oauth/token", nil) {
		t.Error("an API URL that cannot be parsed offers nothing to compare against and must not be trusted")
	}
	if auth.EndpointTrusted("https://api.example", "://not-a-url", nil) {
		t.Error("an endpoint that cannot be parsed must not be trusted")
	}
	if auth.EndpointTrusted("https://api.example", "/oauth/token", nil) {
		t.Error("an endpoint with no host names no origin and must not be trusted")
	}
}

func TestRefreshToken_ErrorsOnOAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "expired"})
	}))
	defer srv.Close()

	_, err := auth.RefreshToken(context.Background(), srv.Client(), srv.URL, "old")
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("expected invalid_grant error, got %v", err)
	}
}

func TestCallbackServer_RejectsMissingCode(t *testing.T) {
	cs, err := auth.NewCallbackServer("state-1")
	if err != nil {
		t.Fatalf("NewCallbackServer: %v", err)
	}
	defer cs.Close()

	resp, err := http.Get(cs.RedirectURI() + "?state=state-1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := cs.Wait(ctx); err == nil {
		t.Fatal("Wait should error when callback has no code")
	}
}

func TestCallbackServer_ChecksStateBeforeError(t *testing.T) {
	cs, err := auth.NewCallbackServer("expected")
	if err != nil {
		t.Fatalf("NewCallbackServer: %v", err)
	}
	defer cs.Close()

	resp, err := http.Get(cs.RedirectURI() + "?error=access_denied&state=WRONG")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = cs.Wait(ctx)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "state") {
		t.Fatalf("state mismatch must be reported before the error param; got %v", err)
	}
}
