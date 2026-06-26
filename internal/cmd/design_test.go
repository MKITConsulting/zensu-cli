package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesignContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/design/context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("componentId"); got != "" {
			t.Errorf("componentId must be absent without --component: got %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"productId": "p1",
			"designMd": []map[string]any{
				{"scope": "product", "name": "Design.md", "content": "## Brand\nUse teal."},
			},
			"css": []map[string]any{
				{"scope": "product", "name": "shared.css", "content": ".btn { color: teal; }"},
			},
			"images": []map[string]any{
				{"id": "img1", "scope": "product", "name": "logo.png"},
			},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewDesignCmd(f)
	if err := runCmd(t, cmd, "context", "p1"); err != nil {
		t.Fatalf("design context error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"Design.md", "Use teal.", "shared.css", ".btn { color: teal; }", "Images:", "SCOPE", "NAME", "ID", "logo.png", "img1"} {
		if !strings.Contains(got, want) {
			t.Errorf("design context output missing %q in:\n%s", want, got)
		}
	}
}

func TestDesignContext_WithComponent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/design/context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("componentId"); got != "c9" {
			t.Errorf("componentId query param: got %q want c9", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"productId": "p1",
			"designMd": []map[string]any{
				{"scope": "component", "name": "Design.md", "content": "Component override"},
			},
			"css":    []map[string]any{},
			"images": []map[string]any{},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewDesignCmd(f)
	if err := runCmd(t, cmd, "context", "p1", "--component", "c9"); err != nil {
		t.Fatalf("design context --component error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"component", "Component override"} {
		if !strings.Contains(got, want) {
			t.Errorf("design context (component) missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Images:") {
		t.Errorf("design context should omit the Images section when there are none, got:\n%s", got)
	}
}

func TestDesignContext_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/design/context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"productId": "p1",
			"designMd":  []map[string]any{{"scope": "product", "name": "Design.md", "content": "hi"}},
			"css":       []map[string]any{},
			"images":    []map[string]any{},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewDesignCmd(f)
	if err := runCmd(t, cmd, "context", "p1", "--json"); err != nil {
		t.Fatalf("design context --json error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"productId": "p1"`) {
		t.Errorf("design context --json should pass through raw JSON, got:\n%s", got)
	}
	if strings.Contains(got, "Images:") {
		t.Errorf("design context --json should not render the human view, got:\n%s", got)
	}
}

func TestDesignContext_RequiresProductArg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without a product id")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewDesignCmd(f)
	if err := runCmd(t, cmd, "context"); err == nil {
		t.Fatal("design context without a product id should error")
	}
}
