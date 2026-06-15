package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoadmapList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/roadmaps" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "r1", "title": "MVP Launch", "period": "2026-Q2", "status": "active"},
			},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1"); err != nil {
		t.Fatalf("roadmap list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "TITLE", "PERIOD", "STATUS", "r1", "MVP Launch", "2026-Q2", "active"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestRoadmapList_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("roadmap list without --product should error")
	}
}

func TestRoadmapGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/roadmaps/r1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r1", "title": "MVP Launch"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "get", "r1"); err != nil {
		t.Fatalf("roadmap get error: %v", err)
	}
	if !strings.Contains(out.String(), "MVP Launch") {
		t.Errorf("get output missing roadmap: %s", out.String())
	}
}

func TestRoadmapCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/roadmaps" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r9", "title": "MVP Launch"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "MVP Launch", "--period", "2026-Q2", "--status", "draft", "--goal", "GA release", "--goal", "billing"); err != nil {
		t.Fatalf("roadmap create error: %v", err)
	}
	if body["title"] != "MVP Launch" || body["period"] != "2026-Q2" || body["status"] != "draft" {
		t.Errorf("create body must carry title, period, status: %v", body)
	}
	goals, ok := body["goals"].([]any)
	if !ok || len(goals) != 2 || goals[0] != "GA release" || goals[1] != "billing" {
		t.Errorf("create body goals must be a JSON array: %v", body["goals"])
	}
	if !strings.Contains(out.String(), "r9") {
		t.Errorf("create output missing created roadmap: %s", out.String())
	}
}

func TestRoadmapCreate_RequiresTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --title is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1"); err == nil {
		t.Fatal("roadmap create without --title should error")
	}
}

func TestRoadmapUpdate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/roadmaps/r1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r1", "title": "Renamed"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "update", "r1", "--title", "Renamed", "--status", "active"); err != nil {
		t.Fatalf("roadmap update error: %v", err)
	}
	if body["title"] != "Renamed" || body["status"] != "active" {
		t.Errorf("update body must carry title and status: %v", body)
	}
}

func TestRoadmapUpdate_RequiresTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --title is missing (backend PUT requires title)")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "update", "r1", "--status", "active"); err == nil {
		t.Fatal("roadmap update without --title should error")
	}
}

func TestRoadmapDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/roadmaps/r1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "delete", "r1"); err != nil {
		t.Fatalf("roadmap delete error: %v", err)
	}
	if !strings.Contains(out.String(), "r1") {
		t.Errorf("delete output should confirm: %s", out.String())
	}
}

func TestRoadmapAddFeature(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/roadmaps/r1/features" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"added": true})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "add-feature", "r1", "--feature", "f1", "--start-period", "2026-Q2", "--end-period", "2026-Q4", "--sort-order", "3"); err != nil {
		t.Fatalf("roadmap add-feature error: %v", err)
	}
	if body["featureId"] != "f1" || body["startPeriod"] != "2026-Q2" || body["endPeriod"] != "2026-Q4" {
		t.Errorf("add-feature body must carry featureId, startPeriod, endPeriod: %v", body)
	}
	if so, ok := body["sortOrder"].(float64); !ok || int(so) != 3 {
		t.Errorf("add-feature body sortOrder must be 3: %v", body["sortOrder"])
	}
}

func TestRoadmapAddFeature_RequiresFeature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --feature is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "add-feature", "r1"); err == nil {
		t.Fatal("roadmap add-feature without --feature should error")
	}
}

func TestRoadmapRemoveFeature(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/roadmaps/r1/features/f1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "remove-feature", "r1", "f1"); err != nil {
		t.Fatalf("roadmap remove-feature error: %v", err)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("remove-feature output should confirm: %s", out.String())
	}
}

func TestRoadmapMilestoneCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/roadmaps/r1/milestones" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "m9", "title": "GA Release"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "milestone-create", "r1", "--title", "GA Release", "--period", "2026-Q3", "--status", "planned"); err != nil {
		t.Fatalf("roadmap milestone-create error: %v", err)
	}
	if body["title"] != "GA Release" || body["period"] != "2026-Q3" || body["status"] != "planned" {
		t.Errorf("milestone-create body must carry title, period, status: %v", body)
	}
	if !strings.Contains(out.String(), "m9") {
		t.Errorf("milestone-create output missing created milestone: %s", out.String())
	}
}

func TestRoadmapMilestoneCreate_RequiresTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --title is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "milestone-create", "r1"); err == nil {
		t.Fatal("roadmap milestone-create without --title should error")
	}
}

func TestRoadmapMilestoneList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/roadmaps/r1/milestones" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "m1", "title": "GA Release", "period": "2026-Q3", "status": "planned"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "milestone-list", "r1"); err != nil {
		t.Fatalf("roadmap milestone-list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "TITLE", "PERIOD", "STATUS", "m1", "GA Release", "2026-Q3", "planned"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestRoadmapMilestoneDelete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/roadmaps/r1/milestones/m1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewRoadmapCmd(f)
	if err := runCmd(t, cmd, "milestone-delete", "r1", "m1"); err != nil {
		t.Fatalf("roadmap milestone-delete error: %v", err)
	}
	if !strings.Contains(out.String(), "m1") {
		t.Errorf("milestone-delete output should confirm: %s", out.String())
	}
}
