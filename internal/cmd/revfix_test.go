package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestBuildBody_InputFileAndStdin(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "body.json")
	if err := os.WriteFile(fp, []byte(`{"k":"v"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := buildBody(nil, fp, nil)
	if err != nil || string(got) != `{"k":"v"}` {
		t.Errorf("file input: got %q err %v", got, err)
	}

	got, err = buildBody(nil, "-", strings.NewReader(`{"s":1}`))
	if err != nil || string(got) != `{"s":1}` {
		t.Errorf("stdin input: got %q err %v", got, err)
	}
}

func TestBuildBody_InvalidField(t *testing.T) {
	if _, err := buildBody([]string{"noequalssign"}, "", nil); err == nil {
		t.Error("field without = should error")
	}
	if _, err := buildBody([]string{"=v"}, "", nil); err == nil {
		t.Error("field with empty key should error")
	}
}

func TestAPICmd_NonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("gateway down"))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewAPICmd(f)
	err := runCmd(t, cmd, "GET", "/api/x")
	if err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "gateway down") {
		t.Fatalf("non-JSON error should surface status+body; got %v", err)
	}
}

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
