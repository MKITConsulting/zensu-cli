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

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
	if ep.Token != srv.URL+"/oauth/token" {
		t.Errorf("downgraded http token_endpoint must be rejected → fallback; got %q", ep.Token)
	}
	if ep.Authorization != srv.URL+"/oauth/authorize" {
		t.Errorf("downgraded http authorization_endpoint must be rejected → fallback; got %q", ep.Authorization)
	}
}

func TestDiscoverEndpoints_AllowsCrossHostHTTPS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "https://mcp.other.example/oauth/authorize",
			"token_endpoint":         "https://mcp.other.example/oauth/token",
		})
	}))
	defer srv.Close()

	ep := auth.DiscoverEndpoints(context.Background(), srv.Client(), srv.URL)
	if ep.Token != "https://mcp.other.example/oauth/token" {
		t.Errorf("cross-host https token_endpoint should be honored; got %q", ep.Token)
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
