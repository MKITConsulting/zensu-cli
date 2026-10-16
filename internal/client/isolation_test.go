package client_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

var fixtureSeq atomic.Int64

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithIsolatedConfigDir(m.Run))
}

func TestConfigDir_IsIsolatedFromRealCredentialStore(t *testing.T) {
	testutil.RequireIsolatedConfigDir(t)
}

func TestDefaultSaver_WritesIntoIsolatedConfigDir(t *testing.T) {
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)

	seq := strconv.FormatInt(fixtureSeq.Add(1), 10)
	wantAccess, wantRefresh := "isolated-acc-"+seq, "isolated-ref-"+seq

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": wantAccess, "refresh_token": wantRefresh, "expires_in": 900})
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer srv.Close()

	before := time.Now()
	cfg := &config.Config{AccessToken: "stale", RefreshToken: "r1", ExpiresAt: before.Add(-time.Minute)}
	if err := cfg.Save(); err != nil {
		t.Fatalf("seeding the stored login: %v", err)
	}
	c := client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client()))
	resp, err := c.Do(context.Background(), http.MethodGet, "/api/products", nil)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	defer resp.Body.Close()

	if cfg.AccessToken != wantAccess {
		t.Fatalf("refresh did not run: in-memory access token is %q", cfg.AccessToken)
	}

	stored := testutil.CredentialStoreEntry(t, dir)
	raw, err := os.ReadFile(stored)
	if err != nil {
		t.Fatalf("reading %s: %v", stored, err)
	}
	var persisted config.Config
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decoding %s: %v", stored, err)
	}
	if persisted.AccessToken != wantAccess {
		t.Errorf("persisted access token: got %q want %q", persisted.AccessToken, wantAccess)
	}
	if persisted.RefreshToken != wantRefresh {
		t.Errorf("persisted refresh token: got %q want %q", persisted.RefreshToken, wantRefresh)
	}
	if !persisted.ExpiresAt.After(before) {
		t.Errorf("persisted expiry %s is not after the refresh started at %s — expires_in was not applied", persisted.ExpiresAt, before)
	}
}
