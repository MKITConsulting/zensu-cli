package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPulseStart(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/pulse/sessions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s9", "head_sha": "abc123"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123", "--branch", "main", "--project", "/repo", "--product", "p1"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	if body["headSha"] != "abc123" || body["branch"] != "main" || body["projectPath"] != "/repo" || body["productId"] != "p1" {
		t.Errorf("start body must carry headSha, branch, projectPath, productId: %v", body)
	}
	if !strings.Contains(out.String(), "s9") {
		t.Errorf("start output missing session id: %s", out.String())
	}
}

func TestPulseStart_RequiresHeadSha(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --head-sha is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start"); err == nil {
		t.Fatal("pulse start without --head-sha should error")
	}
}

func TestPulseEnd(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/pulse/sessions/s1/end" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "ended_at": "2026-01-01T00:00:00Z"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", "s1", "--changed-files", "a.go, b.go"); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	files, ok := body["changedFiles"].([]any)
	if !ok || len(files) != 2 || files[0] != "a.go" || files[1] != "b.go" {
		t.Errorf("end body must carry trimmed changedFiles array: %v", body["changedFiles"])
	}
	if !strings.Contains(out.String(), "s1") {
		t.Errorf("end output missing session id: %s", out.String())
	}
}

func TestPulseEnd_RequiresSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when session id arg is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end"); err == nil {
		t.Fatal("pulse end without a session id should error")
	}
}

func TestPulseSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/pulse/sessions/s1/summary" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session":   map[string]any{"id": "s1"},
			"toolCalls": []map[string]any{{"id": "t1", "tool_name": "create_feature"}},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "summary", "s1"); err != nil {
		t.Fatalf("pulse summary error: %v", err)
	}
	if !strings.Contains(out.String(), "create_feature") {
		t.Errorf("summary output missing tool call: %s", out.String())
	}
}
