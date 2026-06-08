package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestDo_InjectsBearer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer acc-1" {
			t.Errorf("Authorization: got %q want Bearer acc-1", got)
		}
		if r.Header.Get("X-API-Key") != "" {
			t.Error("X-API-Key must not be set when using bearer token")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "acc-1"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d", resp.StatusCode)
	}
}

func TestDo_InjectsAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "zsk_k" {
			t.Errorf("X-API-Key: got %q want zsk_k", got)
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("Authorization must not be set when using api key")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k", AccessToken: "ignored"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
}

func TestDo_RefreshesExpiredTokenBeforeRequest(t *testing.T) {
	var saved bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = r.ParseForm()
			if r.Form.Get("grant_type") != "refresh_token" {
				t.Errorf("refresh grant_type: got %q", r.Form.Get("grant_type"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-acc", "refresh_token": "new-ref", "expires_in": 900})
		case "/api/products":
			if got := r.Header.Get("Authorization"); got != "Bearer new-acc" {
				t.Errorf("expected refreshed bearer, got %q", got)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	now := time.Now()
	cfg := &config.Config{AccessToken: "old-acc", RefreshToken: "old-ref", ExpiresAt: now.Add(-time.Minute)}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token",
		client.WithHTTPClient(srv.Client()),
		client.WithClock(func() time.Time { return now }),
		client.WithSaver(func(*config.Config) error { saved = true; return nil }),
	)
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()
	if cfg.AccessToken != "new-acc" || cfg.RefreshToken != "new-ref" {
		t.Errorf("config not updated after refresh: %+v", cfg)
	}
	if !saved {
		t.Error("expected refreshed tokens to be persisted via saver")
	}
}

func TestDo_RetriesOnce401ThenRefresh(t *testing.T) {
	var apiCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fresh", "refresh_token": "r2", "expires_in": 900})
		case "/api/products":
			apiCalls++
			if r.Header.Get("Authorization") == "Bearer fresh" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r1"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after 401+refresh status: got %d want 200", resp.StatusCode)
	}
	if apiCalls != 2 {
		t.Errorf("expected exactly 2 api calls (401 then retry), got %d", apiCalls)
	}
	if cfg.AccessToken != "fresh" {
		t.Errorf("token not refreshed: %q", cfg.AccessToken)
	}
}

func TestCheckResponse_ParsesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "bad_request", "message": "missing name"})
	}))
	defer srv.Close()

	cfg := &config.Config{APIKey: "zsk_k"}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	err = client.CheckResponse(resp)
	if err == nil {
		t.Fatal("CheckResponse should error on 400")
	}
	apiErr, ok := err.(*client.APIError)
	if !ok {
		t.Fatalf("expected *client.APIError, got %T", err)
	}
	if apiErr.StatusCode != 400 || apiErr.Code != "bad_request" || apiErr.Message != "missing name" {
		t.Errorf("APIError fields wrong: %+v", apiErr)
	}
}
