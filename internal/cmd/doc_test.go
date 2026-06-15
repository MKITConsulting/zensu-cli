package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDocClaudeMd(t *testing.T) {
	var gotVariant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/templates/claude-md" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		gotVariant = r.URL.Query().Get("variant")
		_, _ = w.Write([]byte("# CLAUDE.md\nhello"))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "claude-md", "--product", "p1", "--variant", "full"); err != nil {
		t.Fatalf("doc claude-md error: %v", err)
	}
	if gotVariant != "full" {
		t.Errorf("variant query: got %q want full", gotVariant)
	}
	if !strings.Contains(out.String(), "CLAUDE.md") {
		t.Errorf("claude-md output missing template text: %s", out.String())
	}
}

func TestDocClaudeMd_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "claude-md", "--variant", "full"); err == nil {
		t.Fatal("doc claude-md without --product should error")
	}
}

func TestDocClaudeMd_RequiresVariant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --variant")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "claude-md", "--product", "p1"); err == nil {
		t.Fatal("doc claude-md without --variant should error")
	}
}

func TestDocClaudeMdContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/claude-md-context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"productId": "p1", "name": "Acme"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "claude-md-context", "--product", "p1"); err != nil {
		t.Fatalf("doc claude-md-context error: %v", err)
	}
	if !strings.Contains(out.String(), "Acme") {
		t.Errorf("claude-md-context output missing data: %s", out.String())
	}
}

func TestDocClaudeMdContext_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "claude-md-context"); err == nil {
		t.Fatal("doc claude-md-context without --product should error")
	}
}

func TestDocGenContext(t *testing.T) {
	var gotDocType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/doc-generation-context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		gotDocType = r.URL.Query().Get("docType")
		_ = json.NewEncoder(w).Encode(map[string]any{"feature": map[string]any{"id": "f1"}})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "gen-context", "f1", "--doc-type", "user_facing"); err != nil {
		t.Fatalf("doc gen-context error: %v", err)
	}
	if gotDocType != "user_facing" {
		t.Errorf("docType query: got %q want user_facing", gotDocType)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("gen-context output missing data: %s", out.String())
	}
}

func TestDocGenContext_FeatureFlag(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"feature": map[string]any{"id": "f2"}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "gen-context", "--feature", "f2", "--doc-type", "adr"); err != nil {
		t.Fatalf("doc gen-context error: %v", err)
	}
	if gotPath != "/api/features/f2/doc-generation-context" {
		t.Errorf("path from --feature: got %s", gotPath)
	}
}

func TestDocGenContext_RequiresFeature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without a feature id")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "gen-context", "--doc-type", "user_facing"); err == nil {
		t.Fatal("doc gen-context without a feature id should error")
	}
}

func TestDocGenContext_RequiresDocType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --doc-type")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDocCmd(f)
	if err := runCmd(t, cmd, "gen-context", "f1"); err == nil {
		t.Fatal("doc gen-context without --doc-type should error")
	}
}
