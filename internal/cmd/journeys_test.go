package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJourneysList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/journeys" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "j1", "slug": "checkout", "title": "Checkout", "journey_type": "critical", "priority": "high", "status": "active"},
			},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1"); err != nil {
		t.Fatalf("journeys list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"SLUG", "TITLE", "TYPE", "PRIORITY", "STATUS", "checkout", "Checkout", "critical", "high", "active"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestJourneysList_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("journeys list without --product should error")
	}
}

func TestJourneysGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/journeys/j1" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j1", "slug": "checkout", "title": "Checkout"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "get", "j1", "--product", "p1"); err != nil {
		t.Fatalf("journeys get error: %v", err)
	}
	if !strings.Contains(out.String(), "checkout") {
		t.Errorf("get output missing slug: %s", out.String())
	}
}

func TestJourneysGet_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "get", "j1"); err == nil {
		t.Fatal("journeys get without --product should error")
	}
}

func TestJourneysCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/journeys" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j9", "slug": "my-journey", "title": "Checkout"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "Checkout", "--slug", "my-journey", "--type", "critical", "--priority", "high", "--persona", "buyer", "--tier", "t1", "--description", "Buy flow"); err != nil {
		t.Fatalf("journeys create error: %v", err)
	}
	if body["title"] != "Checkout" || body["slug"] != "my-journey" {
		t.Errorf("create body must carry title + slug: %v", body)
	}
	if body["journeyType"] != "critical" || body["priority"] != "high" || body["persona"] != "buyer" || body["tierId"] != "t1" || body["description"] != "Buy flow" {
		t.Errorf("create body must map optional flags to wire keys journeyType/priority/persona/tierId/description: %v", body)
	}
	if !strings.Contains(out.String(), "j9") && !strings.Contains(out.String(), "my-journey") {
		t.Errorf("create output missing created journey: %s", out.String())
	}
}

func TestJourneysCreate_DerivesSlugFromTitle(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "j9"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "Guest Checkout Flow!"); err != nil {
		t.Fatalf("journeys create error: %v", err)
	}
	if body["slug"] != "guest-checkout-flow" {
		t.Errorf("slug should be derived from title: got %v want guest-checkout-flow", body["slug"])
	}
}

func TestJourneysCreate_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "create", "--title", "Checkout"); err == nil {
		t.Fatal("journeys create without --product should error")
	}
}

func TestJourneysCreate_RequiresTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --title is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1"); err == nil {
		t.Fatal("journeys create without --title should error")
	}
}

func TestJourneysStep(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/journeys/j1/steps" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "s1", "step_order": 1, "title": "Open cart"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step", "j1", "--product", "p1", "--title", "Open cart", "--step-order", "1", "--feature", "f1", "--interaction-type", "navigation", "--expected-result", "cart shown", "--critical"); err != nil {
		t.Fatalf("journeys step error: %v", err)
	}
	if body["title"] != "Open cart" {
		t.Errorf("step body must carry title: %v", body)
	}
	if body["stepOrder"] != float64(1) {
		t.Errorf("step body must carry stepOrder: %v", body["stepOrder"])
	}
	if body["featureId"] != "f1" || body["interactionType"] != "navigation" || body["expectedResult"] != "cart shown" {
		t.Errorf("step body must map optional flags to wire keys featureId/interactionType/expectedResult: %v", body)
	}
	if body["isCritical"] != true {
		t.Errorf("step body must carry isCritical: %v", body["isCritical"])
	}
	if !strings.Contains(out.String(), "Open cart") {
		t.Errorf("step output missing step title: %s", out.String())
	}
}

func TestJourneysStep_RequiresStepOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --step-order is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step", "j1", "--product", "p1", "--title", "Open cart"); err == nil {
		t.Fatal("journeys step without --step-order should error")
	}
}

func TestJourneysStep_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step", "j1", "--title", "Open cart", "--step-order", "1"); err == nil {
		t.Fatal("journeys step without --product should error")
	}
}

func TestJourneysSteps_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/journeys/j1/steps" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "s1", "step_order": 1, "title": "Open cart", "interaction_type": "navigation"},
			},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "steps", "j1", "--product", "p1"); err != nil {
		t.Fatalf("journeys steps error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ORDER", "TITLE", "INTERACTION", "Open cart", "navigation"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func TestJourneysSteps_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "steps", "j1"); err == nil {
		t.Fatal("journeys steps without --product should error")
	}
}

func TestJourneysHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/journeys/j1/health" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"journeyId": "j1", "score": 0.8, "status": "healthy"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "health", "j1", "--product", "p1"); err != nil {
		t.Fatalf("journeys health error: %v", err)
	}
	if !strings.Contains(out.String(), "healthy") {
		t.Errorf("health output missing status: %s", out.String())
	}
}

func TestJourneysHealth_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "health", "j1"); err == nil {
		t.Fatal("journeys health without --product should error")
	}
}

func TestJourneysSuggest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/journeys/context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ghostScanCount": 2, "features": []string{"f1"}})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "suggest", "--product", "p1"); err != nil {
		t.Fatalf("journeys suggest error: %v", err)
	}
	if !strings.Contains(out.String(), "ghostScanCount") {
		t.Errorf("suggest output missing context payload: %s", out.String())
	}
}

func TestJourneysSuggest_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "suggest"); err == nil {
		t.Fatal("journeys suggest without --product should error")
	}
}
