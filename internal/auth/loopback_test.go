package auth_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
)

func TestCallbackServer_RedirectURIIsLoopback(t *testing.T) {
	cs, err := auth.NewCallbackServer("state-1")
	if err != nil {
		t.Fatalf("NewCallbackServer error: %v", err)
	}
	defer cs.Close()

	uri := cs.RedirectURI()
	if !strings.HasPrefix(uri, "http://127.0.0.1:") || !strings.HasSuffix(uri, "/callback") {
		t.Errorf("RedirectURI not loopback /callback: %q", uri)
	}
}

func TestCallbackServer_CapturesCode(t *testing.T) {
	cs, err := auth.NewCallbackServer("state-1")
	if err != nil {
		t.Fatalf("NewCallbackServer error: %v", err)
	}
	defer cs.Close()

	resp, err := http.Get(cs.RedirectURI() + "?code=auth-code-42&state=state-1")
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("success status: got %d", resp.StatusCode)
	}
	if !strings.Contains(strings.ToLower(string(body)), "success") {
		t.Errorf("success page should mention success, got %q", string(body))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	code, err := cs.Wait(ctx)
	if err != nil {
		t.Fatalf("Wait error: %v", err)
	}
	if code != "auth-code-42" {
		t.Errorf("code: got %q want auth-code-42", code)
	}
}

func TestCallbackServer_RejectsStateMismatch(t *testing.T) {
	cs, err := auth.NewCallbackServer("expected-state")
	if err != nil {
		t.Fatalf("NewCallbackServer error: %v", err)
	}
	defer cs.Close()

	resp, err := http.Get(cs.RedirectURI() + "?code=x&state=WRONG")
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = cs.Wait(ctx)
	if err == nil {
		t.Fatal("Wait should error on state mismatch, got nil")
	}
}

func TestCallbackServer_PropagatesOAuthError(t *testing.T) {
	cs, err := auth.NewCallbackServer("state-1")
	if err != nil {
		t.Fatalf("NewCallbackServer error: %v", err)
	}
	defer cs.Close()

	resp, err := http.Get(cs.RedirectURI() + "?error=access_denied&state=state-1")
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = cs.Wait(ctx)
	if err == nil {
		t.Fatal("Wait should error when callback carries ?error=, got nil")
	}
}
