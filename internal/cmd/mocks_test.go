package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMocksList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "m1", "feature_id": "f1", "mock_type": "html", "title": "Login screen", "file_name": "login.html", "mime_type": "text/html", "file_size_bytes": 128},
				{"id": "m2", "feature_id": "f1", "mock_type": "image", "title": nil, "file_name": "hero.png", "mime_type": "image/png", "file_size_bytes": 4096},
			},
			"total":  2,
			"limit":  50,
			"offset": 0,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1"); err != nil {
		t.Fatalf("mocks list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"TYPE", "TITLE", "FILE_NAME", "ID", "html", "Login screen", "login.html", "m1", "image", "hero.png", "m2"} {
		if !strings.Contains(got, want) {
			t.Errorf("mocks list table missing %q in:\n%s", want, got)
		}
	}
}

func TestMocksList_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"id": "m1", "mock_type": "html", "file_name": "login.html"}},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list", "f1", "--json"); err != nil {
		t.Fatalf("mocks list --json error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"file_name": "login.html"`) {
		t.Errorf("mocks list --json should pass through raw JSON, got:\n%s", got)
	}
	if strings.Contains(got, "FILE_NAME") {
		t.Errorf("mocks list --json should not render the table header, got:\n%s", got)
	}
}

func TestMocksList_RequiresFeatureArg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without a feature id")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("mocks list without a feature id should error")
	}
}

func TestMocksGet_Metadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks" {
			t.Errorf("metadata path should hit the list endpoint, got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "m1", "feature_id": "f1", "mock_type": "image", "title": "Hero", "file_name": "hero.png", "mime_type": "image/png", "file_size_bytes": 4096},
				{"id": "m2", "feature_id": "f1", "mock_type": "html", "title": "Login", "file_name": "login.html", "mime_type": "text/html", "file_size_bytes": 128},
			},
			"total": 2,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m2"); err != nil {
		t.Fatalf("mocks get error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"m2", "html", "Login", "login.html", "text/html", "128 bytes"} {
		if !strings.Contains(got, want) {
			t.Errorf("mocks get metadata missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "hero.png") {
		t.Errorf("mocks get should only print the requested mock, leaked sibling in:\n%s", got)
	}
}

func TestMocksGet_Raw(t *testing.T) {
	const htmlBody = "<!doctype html><html><body><h1>Login</h1></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/mocks/m2/raw" {
			t.Errorf("--raw should hit the raw endpoint, got %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "m2", "--raw"); err != nil {
		t.Fatalf("mocks get --raw error: %v", err)
	}
	if got := out.String(); got != htmlBody {
		t.Errorf("mocks get --raw should write the body verbatim:\n got: %q\nwant: %q", got, htmlBody)
	}
}

func TestMocksGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":  []map[string]any{{"id": "m1", "mock_type": "html", "file_name": "login.html"}},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1", "does-not-exist"); err == nil {
		t.Fatal("mocks get for an unknown mock id should error")
	}
}

func TestMocksGet_RequiresBothArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without both ids")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewMocksCmd(f)
	if err := runCmd(t, cmd, "get", "f1"); err == nil {
		t.Fatal("mocks get without a mock id should error")
	}
}
