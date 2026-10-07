package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWikiList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/wiki/pages" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "w1", "slug": "getting-started", "title": "Getting Started", "audience": "developer"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1"); err != nil {
		t.Fatalf("wiki list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "SLUG", "TITLE", "AUDIENCE", "getting-started", "Getting Started", "developer"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestWikiList_WarnsWhenTheServerLimitIsReached(t *testing.T) {
	tests := []struct {
		name        string
		pages       int
		json        bool
		wantWarning bool
	}{
		{"below the server limit", wikiPageLimit - 1, false, false},
		{"at the server limit", wikiPageLimit, false, true},
		{"at the server limit with --json", wikiPageLimit, true, true},
		{"more than the limit, so the server pages for itself", wikiPageLimit + 1, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pages := make([]map[string]any, tc.pages)
			for i := range pages {
				pages[i] = map[string]any{"id": fmt.Sprintf("w%03d", i), "slug": fmt.Sprintf("page-%03d", i), "title": "T", "audience": "developer"}
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_ = json.NewEncoder(w).Encode(pages)
			}))
			defer srv.Close()

			f, out := testFactory(srv)
			cmd := NewWikiCmd(f)
			var stderr bytes.Buffer
			cmd.SetErr(&stderr)
			args := []string{"list", "--product", "p1"}
			if tc.json {
				args = append(args, "--json")
			}
			if err := runCmd(t, cmd, args...); err != nil {
				t.Fatalf("wiki list error: %v", err)
			}
			if !strings.Contains(out.String(), fmt.Sprintf("page-%03d", tc.pages-1)) {
				t.Errorf("the last page is missing from stdout:\n%s", out.String())
			}
			if strings.Contains(out.String(), "warning") {
				t.Errorf("the warning must not reach stdout:\n%s", out.String())
			}
			if got := strings.Contains(stderr.String(), "at most 50 wiki pages per query"); got != tc.wantWarning {
				t.Errorf("limit warning on stderr: got %t, want %t (stderr %q)", got, tc.wantWarning, stderr.String())
			}
		})
	}
}

func TestWikiList_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called without --product, which it would reject")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	err := runCmd(t, NewWikiCmd(f), "list", "--audience", "developer")
	if err == nil || !strings.Contains(err.Error(), "--product is required") {
		t.Fatalf("wiki list without --product must fail before the request, got: %v", err)
	}
}

func TestWikiList_Filters(t *testing.T) {
	var q map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = map[string]string{
			"productId":    r.URL.Query().Get("productId"),
			"audience":     r.URL.Query().Get("audience"),
			"parentPageId": r.URL.Query().Get("parentPageId"),
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1", "--audience", "developer", "--parent", "pp1"); err != nil {
		t.Fatalf("wiki list error: %v", err)
	}
	if q["productId"] != "p1" {
		t.Errorf("productId query: got %q want p1", q["productId"])
	}
	if q["audience"] != "developer" {
		t.Errorf("audience query: got %q want developer", q["audience"])
	}
	if q["parentPageId"] != "pp1" {
		t.Errorf("parentPageId query: got %q want pp1", q["parentPageId"])
	}
}

func TestWikiCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/wiki/pages" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "w9", "slug": "guide", "title": "Guide"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "create",
		"--product", "p1",
		"--title", "Guide",
		"--content", "# Hello",
		"--audience", "developer",
		"--visibility", "public",
	); err != nil {
		t.Fatalf("wiki create error: %v", err)
	}
	if body["productId"] != "p1" || body["title"] != "Guide" || body["content"] != "# Hello" {
		t.Errorf("create body must carry productId, title, content: %v", body)
	}
	if body["audience"] != "developer" || body["visibility"] != "public" {
		t.Errorf("create body must carry optional audience+visibility: %v", body)
	}
	if !strings.Contains(out.String(), "w9") && !strings.Contains(out.String(), "guide") {
		t.Errorf("create output missing created page: %s", out.String())
	}
}

func TestWikiCreate_OmitsUnsetOptionals(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "w9"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "Guide", "--content", "x"); err != nil {
		t.Fatalf("wiki create error: %v", err)
	}
	if _, ok := body["visibility"]; ok {
		t.Errorf("unset --visibility must not appear in body: %v", body)
	}
	if _, ok := body["entityId"]; ok {
		t.Errorf("unset --entity-id must not appear in body: %v", body)
	}
}

func TestWikiCreate_RequiresContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --content is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "Guide"); err == nil {
		t.Fatal("wiki create without --content should error")
	}
}

func TestWikiUpdate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/wiki/pages/w1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "w1", "title": "NewTitle"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "update", "w1", "--title", "NewTitle", "--change-summary", "fix typo"); err != nil {
		t.Fatalf("wiki update error: %v", err)
	}
	if body["title"] != "NewTitle" {
		t.Errorf("update title: got %v want NewTitle", body["title"])
	}
	if body["changeSummary"] != "fix typo" {
		t.Errorf("update must send changeSummary wire key: got %v", body["changeSummary"])
	}
	if !strings.Contains(out.String(), "w1") {
		t.Errorf("update output should confirm page id: %s", out.String())
	}
}

func TestWikiUpdate_RequiresAField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no update flags are passed")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewWikiCmd(f)
	if err := runCmd(t, cmd, "update", "w1"); err == nil {
		t.Fatal("wiki update with no flags should error")
	}
}
