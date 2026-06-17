package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestFeaturesList_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = writeRaw(w, `{"data":[{"id":"f1","slug":"ZEN-001","title":"Login"}],"total":1}`)
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1", "--json"); err != nil {
		t.Fatalf("features list --json: %v", err)
	}
	if !strings.Contains(out.String(), "\"data\"") {
		t.Errorf("--json should emit raw envelope, got:\n%s", out.String())
	}
}

func TestProductsCreate_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = writeRaw(w, `{"id":"p9","name":"Gamma","product_type":"public"}`)
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewProductsCmd(f)
	if err := runCmd(t, cmd, "create", "--name", "Gamma", "--json"); err != nil {
		t.Fatalf("products create --json: %v", err)
	}
	if !strings.Contains(out.String(), "\"product_type\"") {
		t.Errorf("--json should emit raw created object, got:\n%s", out.String())
	}
}

func TestLoginWithToken_StdinAndEmpty(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") == "zsk_valid" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	f := &Factory{Out: &strings.Builder{}}
	httpClient := srv.Client()
	cfg := &config.Config{}
	if err := f.loginWithToken(context.Background(), httpClient, cfg, srv.URL, "-", strings.NewReader("zsk_valid\n")); err != nil {
		t.Fatalf("stdin token login: %v", err)
	}
	if cfg.APIKey != "zsk_valid" {
		t.Errorf("stdin key not persisted: %q", cfg.APIKey)
	}

	cfg2 := &config.Config{}
	if err := f.loginWithToken(context.Background(), httpClient, cfg2, srv.URL, "", strings.NewReader("")); err == nil {
		t.Error("empty token should error")
	}
}

func writeRaw(w http.ResponseWriter, s string) error {
	_, err := w.Write([]byte(s))
	return err
}
