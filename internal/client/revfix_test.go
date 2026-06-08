package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestCheckResponse_NonJSONBodyFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("gateway down"))
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	err = client.CheckResponse(resp)
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("want *APIError, got %T", err)
	}
	if apiErr.StatusCode != 500 || !strings.Contains(apiErr.Message, "gateway down") {
		t.Errorf("non-JSON fallback wrong: %+v", apiErr)
	}
}

func TestDo_RefreshFailsWhenNoRefreshToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{AccessToken: "stale", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
	)
	_, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err == nil || !strings.Contains(err.Error(), "zensu auth login") {
		t.Fatalf("expired token with no refresh token should error pointing to login; got %v", err)
	}
}

func TestDo_RefreshPropagatesTokenEndpointError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
		client.WithSaver(func(*config.Config) error { return nil }),
	)
	_, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err == nil || !strings.Contains(err.Error(), "refreshing session") {
		t.Fatalf("token-endpoint failure should surface as refresh error; got %v", err)
	}
}
