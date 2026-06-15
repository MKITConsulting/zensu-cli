package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOrgUsers_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/members" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "u1", "email": "ada@example.com", "name": "Ada Lovelace", "role": "admin"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewOrgCmd(f)
	if err := runCmd(t, cmd, "users"); err != nil {
		t.Fatalf("org users error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "EMAIL", "NAME", "ROLE", "u1", "ada@example.com", "Ada Lovelace", "admin"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestOrgUsers_QueryFilter(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewOrgCmd(f)
	if err := runCmd(t, cmd, "users", "--query", "ada"); err != nil {
		t.Fatalf("org users error: %v", err)
	}
	if gotQuery != "ada" {
		t.Errorf("query filter not sent as q: got %q want ada", gotQuery)
	}
}

func TestOrgUsers_NoQueryOmitsParam(t *testing.T) {
	var rawQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewOrgCmd(f)
	if err := runCmd(t, cmd, "users"); err != nil {
		t.Fatalf("org users error: %v", err)
	}
	if rawQuery != "" {
		t.Errorf("no --query should omit the q param entirely, got raw query %q", rawQuery)
	}
}

func TestOrgUsers_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "u1", "email": "ada@example.com", "name": "Ada Lovelace", "role": "admin"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewOrgCmd(f)
	if err := runCmd(t, cmd, "users", "--json"); err != nil {
		t.Fatalf("org users --json error: %v", err)
	}
	if !strings.Contains(out.String(), "ada@example.com") {
		t.Errorf("json output missing member: %s", out.String())
	}
}
