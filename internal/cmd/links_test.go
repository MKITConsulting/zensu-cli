package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLinkTest(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/tests" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t1"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "test", "f1", "--test-type", "unit", "--file", "foo_test.go", "--function", "TestFoo", "--last-run-status", "passed"); err != nil {
		t.Fatalf("link test error: %v", err)
	}
	if body["testType"] != "unit" || body["filePath"] != "foo_test.go" || body["functionName"] != "TestFoo" || body["lastRunStatus"] != "passed" {
		t.Errorf("link test body must carry testType, filePath, functionName, lastRunStatus: %v", body)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("link test output missing feature id: %s", out.String())
	}
}

func TestLinkTest_RequiresTestType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --test-type is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "test", "f1", "--file", "foo_test.go"); err == nil {
		t.Fatal("link test without --test-type should error")
	}
}

func TestLinkTest_RequiresFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --file is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "test", "f1", "--test-type", "unit"); err == nil {
		t.Fatal("link test without --file should error")
	}
}

func TestLinkDocs(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/docs" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "d1"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "docs", "f1", "--doc-type", "api_reference", "--title", "API", "--external-url", "https://x/y"); err != nil {
		t.Fatalf("link docs error: %v", err)
	}
	if body["docType"] != "api_reference" || body["title"] != "API" || body["externalUrl"] != "https://x/y" {
		t.Errorf("link docs body must carry docType, title, externalUrl: %v", body)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("link docs output missing feature id: %s", out.String())
	}
}

func TestLinkDocs_RequiresDocType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --doc-type is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "docs", "f1", "--title", "API"); err == nil {
		t.Fatal("link docs without --doc-type should error")
	}
}

func TestLinkSource(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/source-files/bulk" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"created": 2, "updated": 0})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "source", "f1", "--file", "a.go:source:go", "--file", "b.go"); err != nil {
		t.Fatalf("link source error: %v", err)
	}
	filesRaw, ok := body["files"].([]any)
	if !ok || len(filesRaw) != 2 {
		t.Fatalf("link source body must carry a files array of 2 entries: %v", body)
	}
	first, _ := filesRaw[0].(map[string]any)
	if first["filePath"] != "a.go" || first["fileType"] != "source" || first["language"] != "go" {
		t.Errorf("first source file entry must carry filePath, fileType, language: %v", first)
	}
	second, _ := filesRaw[1].(map[string]any)
	if second["filePath"] != "b.go" {
		t.Errorf("second source file entry must carry filePath: %v", second)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("link source output missing feature id: %s", out.String())
	}
}

func TestLinkSource_RequiresFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no --file is given")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewLinkCmd(f)
	if err := runCmd(t, cmd, "source", "f1"); err == nil {
		t.Fatal("link source without --file should error")
	}
}
