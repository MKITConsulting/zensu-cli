package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTiersCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/tiers" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t9", "slug": "pro", "name": "Pro"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--slug", "pro", "--name", "Pro", "--tier-order", "2", "--default", "--color", "#abc"); err != nil {
		t.Fatalf("tiers create error: %v", err)
	}
	if body["slug"] != "pro" || body["name"] != "Pro" {
		t.Errorf("create body must carry slug + name: %v", body)
	}
	if v, ok := body["tierOrder"].(float64); !ok || v != 2 {
		t.Errorf("create body must carry tierOrder=2 (camelCase wire key): %v", body)
	}
	if body["isDefault"] != true {
		t.Errorf("create body must carry isDefault=true when --default set: %v", body)
	}
	if body["color"] != "#abc" {
		t.Errorf("create body must carry color: %v", body)
	}
	if !strings.Contains(out.String(), "t9") && !strings.Contains(out.String(), "pro") {
		t.Errorf("create output missing created tier: %s", out.String())
	}
}

func TestTiersCreate_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "create", "--slug", "pro", "--name", "Pro", "--tier-order", "2"); err == nil {
		t.Fatal("tiers create without --product should error")
	}
}

func TestTiersCreate_RequiresTierOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --tier-order is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--slug", "pro", "--name", "Pro"); err == nil {
		t.Fatal("tiers create without --tier-order should error")
	}
}

func TestTiersList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/tiers" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "t1", "slug": "free", "name": "Free", "tier_order": 1, "is_default": true},
			},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1"); err != nil {
		t.Fatalf("tiers list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ORDER", "SLUG", "NAME", "DEFAULT", "free", "Free"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestTiersList_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("tiers list without --product should error")
	}
}

func TestTiersMatrix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/tier-matrix" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tiers":    []map[string]any{{"id": "t1", "slug": "free"}},
			"features": []map[string]any{{"id": "f1"}},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "matrix", "--product", "p1"); err != nil {
		t.Fatalf("tiers matrix error: %v", err)
	}
	if !strings.Contains(out.String(), "free") {
		t.Errorf("matrix output missing tier data: %s", out.String())
	}
}

func TestTiersMatrix_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "matrix"); err == nil {
		t.Fatal("tiers matrix without --product should error")
	}
}

func TestTiersSetFeature(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/features/f1/tiers" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewTiersCmd(f)
	tiersJSON := `[{"tierId":"t1","gatingType":"hard"},{"tierId":"t2","gatingType":"soft","tierLimits":{"seats":5}}]`
	if err := runCmd(t, cmd, "set-feature", "f1", "--tiers", tiersJSON); err != nil {
		t.Fatalf("tiers set-feature error: %v", err)
	}
	arr, ok := body["tiers"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("body must carry a tiers array of 2 entries: %v", body)
	}
	first, _ := arr[0].(map[string]any)
	if first["tierId"] != "t1" || first["gatingType"] != "hard" {
		t.Errorf("first tier entry must carry tierId + gatingType wire keys: %v", first)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("set-feature output missing confirmation: %s", out.String())
	}
}

func TestTiersSetFeature_RequiresTiers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --tiers is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "set-feature", "f1"); err == nil {
		t.Fatal("tiers set-feature without --tiers should error")
	}
}

func TestTiersSetFeature_RejectsInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --tiers is not valid JSON")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewTiersCmd(f)
	if err := runCmd(t, cmd, "set-feature", "f1", "--tiers", "not-json"); err == nil {
		t.Fatal("tiers set-feature with invalid --tiers JSON should error before sending a request")
	}
}
