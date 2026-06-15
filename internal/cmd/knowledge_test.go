package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeSearch(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/knowledge/search" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]any{
				{"itemId": "i1", "sourceType": "feature", "title": "Login", "score": 0.91},
			},
			"mode": "hybrid",
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewKnowledgeCmd(f)
	if err := runCmd(t, cmd, "search", "--query", "auth flow", "--scope", "org", "--limit", "5"); err != nil {
		t.Fatalf("knowledge search error: %v", err)
	}
	if body["query"] != "auth flow" {
		t.Errorf("search body must carry query: got %v", body["query"])
	}
	if body["scope"] != "org" {
		t.Errorf("search body must carry scope when set: got %v", body["scope"])
	}
	if body["limit"] != float64(5) {
		t.Errorf("search body must carry limit when set: got %v", body["limit"])
	}
	got := out.String()
	for _, want := range []string{"SCORE", "SOURCE", "TITLE", "Login", "feature", "i1"} {
		if !strings.Contains(got, want) {
			t.Errorf("search table missing %q in:\n%s", want, got)
		}
	}
}

func TestKnowledgeSearch_OmitsUnsetOptionals(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{}, "mode": "hybrid"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewKnowledgeCmd(f)
	if err := runCmd(t, cmd, "search", "--query", "auth flow"); err != nil {
		t.Fatalf("knowledge search error: %v", err)
	}
	if _, ok := body["scope"]; ok {
		t.Errorf("scope must be omitted when --scope is not set: got %v", body["scope"])
	}
	if _, ok := body["limit"]; ok {
		t.Errorf("limit must be omitted when --limit is not set: got %v", body["limit"])
	}
}

func TestKnowledgeSearch_RequiresQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --query is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewKnowledgeCmd(f)
	if err := runCmd(t, cmd, "search"); err == nil {
		t.Fatal("knowledge search without --query should error")
	}
}

func TestKnowledgeGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/knowledge/items/i1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "i1", "title": "Login", "content": "full text"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewKnowledgeCmd(f)
	if err := runCmd(t, cmd, "get", "i1"); err != nil {
		t.Fatalf("knowledge get error: %v", err)
	}
	if !strings.Contains(out.String(), "full text") {
		t.Errorf("get output missing item content: %s", out.String())
	}
}

func TestKnowledgeSources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/knowledge/sources" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "s1", "name": "Roadmap", "source_type": "internal", "sync_status": "synced"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewKnowledgeCmd(f)
	if err := runCmd(t, cmd, "sources"); err != nil {
		t.Fatalf("knowledge sources error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"NAME", "TYPE", "SYNC", "Roadmap", "internal", "synced", "s1"} {
		if !strings.Contains(got, want) {
			t.Errorf("sources table missing %q in:\n%s", want, got)
		}
	}
}
