package client_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestWithTimeout_ReturnsACopyThatKeepsEverythingButTheTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			<-release
		}
		if r.Header.Get("Authorization") != "Bearer zst_EXAMPLE_token" {
			t.Errorf("authorization = %q, want the copied credential", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	defer close(release)

	base := client.New(&config.Config{AccessToken: "zst_EXAMPLE_token"}, srv.URL, "", client.WithHTTPClient(srv.Client()))
	short := base.WithTimeout(100 * time.Millisecond)
	if short == base || short.HTTPClient == base.HTTPClient {
		t.Fatal("WithTimeout must return a copy with its own HTTP client")
	}
	if short.HTTPClient.Timeout != 100*time.Millisecond || base.HTTPClient.Timeout != 0 {
		t.Fatalf("timeouts = %v on the copy and %v on the original, want 100ms and the original untouched", short.HTTPClient.Timeout, base.HTTPClient.Timeout)
	}
	if short.BaseURL != base.BaseURL || short.AuthMode() != client.AuthModeSessionToken || short.HTTPClient.CheckRedirect == nil || short.HTTPClient.Transport != base.HTTPClient.Transport {
		t.Fatal("the copy must keep the host, the credential, the redirect guard and the transport")
	}

	_, err := short.Do(context.Background(), http.MethodGet, "/slow", nil)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("error = %v, want the copy's timeout", err)
	}
	resp, err := base.Do(context.Background(), http.MethodGet, "/fast", nil)
	if err != nil {
		t.Fatalf("original client: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestWithTimeout_LeavesTheDefaultTimeoutOfTheOriginal(t *testing.T) {
	base := client.New(&config.Config{APIKey: "zsk_test"}, "http://zensu.test", "")
	long := base.WithTimeout(65 * time.Second)
	if long.HTTPClient.Timeout != 65*time.Second || base.HTTPClient.Timeout != 30*time.Second {
		t.Fatalf("timeouts = %v and %v, want 65s on the copy and the 30s default on the original", long.HTTPClient.Timeout, base.HTTPClient.Timeout)
	}
	if long.AuthMode() != client.AuthModeAPIKey {
		t.Fatalf("auth mode = %q", long.AuthMode())
	}
}
