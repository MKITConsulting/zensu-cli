package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubfeaturesList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/feat1/subfeatures" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "s1", "slug": "login-form", "title": "Login Form", "status": "planned"},
			},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "list", "--feature", "feat1"); err != nil {
		t.Fatalf("subfeatures list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ZEN", "TITLE", "STATUS", "login-form", "Login Form", "planned"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestSubfeaturesList_PositionalFeatureID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}, "total": 0})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "list", "feat1"); err != nil {
		t.Fatalf("subfeatures list error: %v", err)
	}
	if gotPath != "/api/features/feat1/subfeatures" {
		t.Errorf("positional feature id path: got %q", gotPath)
	}
}

func TestSubfeaturesList_CompactView(t *testing.T) {
	var gotView string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotView = r.URL.Query().Get("view")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}, "total": 0})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "list", "--feature", "feat1", "--compact"); err != nil {
		t.Fatalf("subfeatures list error: %v", err)
	}
	if gotView != "compact" {
		t.Errorf("compact view query not sent: got %q", gotView)
	}
}

func TestSubfeaturesList_RequiresFeature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without a feature id")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("subfeatures list without --feature should error")
	}
}

func TestSubfeaturesAdd(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/feat1/subfeatures" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s9", "slug": "login-form", "title": "Login Form"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "add", "--feature", "feat1", "--title", "Login Form", "--slug", "login-form", "--priority", "high"); err != nil {
		t.Fatalf("subfeatures add error: %v", err)
	}
	if body["slug"] != "login-form" || body["title"] != "Login Form" || body["priority"] != "high" {
		t.Errorf("add body must carry slug, title, priority: %v", body)
	}
	if !strings.Contains(out.String(), "s9") && !strings.Contains(out.String(), "login-form") {
		t.Errorf("add output missing created sub-feature: %s", out.String())
	}
}

func TestSubfeaturesAdd_DerivesSlugFromTitle(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s9"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "add", "feat1", "--title", "Reset Password Flow!"); err != nil {
		t.Fatalf("subfeatures add error: %v", err)
	}
	if body["slug"] != "reset-password-flow" {
		t.Errorf("slug should be derived from title: got %v want reset-password-flow", body["slug"])
	}
}

func TestSubfeaturesAdd_RequiresTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --title is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "add", "--feature", "feat1"); err == nil {
		t.Fatal("subfeatures add without --title should error")
	}
}

func TestSubfeaturesAdd_RequiresFeature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no feature id is given")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "add", "--title", "Login"); err == nil {
		t.Fatal("subfeatures add without a feature id should error")
	}
}

func TestSubfeaturesPromote(t *testing.T) {
	var gotPath string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "slug": "login-form"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "promote", "--feature", "feat1", "s1"); err != nil {
		t.Fatalf("subfeatures promote error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("promote method: got %s want POST", gotMethod)
	}
	if gotPath != "/api/features/feat1/subfeatures/s1/promote" {
		t.Errorf("promote path: got %q", gotPath)
	}
	if !strings.Contains(out.String(), "s1") {
		t.Errorf("promote output should confirm the sub-feature: %s", out.String())
	}
}

func TestSubfeaturesPromote_PositionalFeatureAndSub(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "promote", "feat1", "s1"); err != nil {
		t.Fatalf("subfeatures promote error: %v", err)
	}
	if gotPath != "/api/features/feat1/subfeatures/s1/promote" {
		t.Errorf("promote path with positional feature id: got %q", gotPath)
	}
}

func TestSubfeaturesPromote_RequiresSubID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when the subfeature id is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSubfeaturesCmd(f)
	if err := runCmd(t, cmd, "promote", "--feature", "feat1"); err == nil {
		t.Fatal("subfeatures promote without a subfeature id should error")
	}
}
