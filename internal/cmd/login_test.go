package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestLoginWithToken_ValidatesAndPersists(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "zsk_valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "login", "--api-url", srv.URL, "--with-token", "zsk_valid"); err != nil {
		t.Fatalf("login --with-token error: %v", err)
	}
	if !strings.Contains(out.String(), "API key") {
		t.Errorf("expected success message, got %q", out.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.APIKey != "zsk_valid" {
		t.Errorf("APIKey not persisted: %+v", cfg)
	}
	if cfg.APIURL != srv.URL {
		t.Errorf("APIURL: got %q want %q", cfg.APIURL, srv.URL)
	}
}

func TestLoginWithToken_RejectsBadKey(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	f, _ := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "login", "--api-url", srv.URL, "--with-token", "zsk_bad"); err == nil {
		t.Fatal("login with invalid key should error")
	}
}
