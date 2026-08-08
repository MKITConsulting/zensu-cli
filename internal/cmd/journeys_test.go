package cmd

import (
	"encoding/json"
	"fmt"
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

func decoyStep(id string, order int) map[string]any {
	return map[string]any{
		"id": id, "step_order": order, "title": "decoy " + id,
		"feature_id": "feat-" + id, "description": "decoy description",
		"interaction_type": "action", "expected_result": "decoy result", "is_critical": false,
	}
}

func stepUpdateServer(t *testing.T, existing map[string]any, put func(map[string]any)) (*httptest.Server, *string, *string) {
	t.Helper()
	gotMethod, gotPath := new(string), new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if r.URL.EscapedPath() != "/api/products/p1/journeys/j1/steps" {
				t.Errorf("the read leg must GET the journey's step list, got %s", r.URL.EscapedPath())
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
				decoyStep("before", 1), existing, decoyStep("after", 9),
			}})
			return
		}
		*gotMethod, *gotPath = r.Method, r.URL.Path
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		put(body)
		merged := map[string]any{"id": existing["id"]}
		for k, v := range map[string]string{"stepOrder": "step_order", "title": "title", "featureId": "feature_id", "description": "description", "interactionType": "interaction_type", "expectedResult": "expected_result", "isCritical": "is_critical"} {
			merged[v] = body[k]
		}
		_ = json.NewEncoder(w).Encode(merged)
	}))
	return srv, gotMethod, gotPath
}

func fullStep() map[string]any {
	return map[string]any{
		"id": "s1", "step_order": 4, "title": "Server title",
		"feature_id": "feat-existing", "description": "existing description",
		"interaction_type": "output", "expected_result": "existing result", "is_critical": true,
	}
}

func TestJourneysStepUpdate_ResendsEveryFieldItDidNotChange(t *testing.T) {
	var body map[string]any
	srv, gotMethod, gotPath := stepUpdateServer(t, fullStep(), func(b map[string]any) { body = b })
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--description", "only this"); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	if *gotMethod != http.MethodPut || *gotPath != "/api/products/p1/journeys/j1/steps/s1" {
		t.Errorf("step-update must PUT the step path, got %s %s", *gotMethod, *gotPath)
	}
	if body["description"] != "only this" {
		t.Errorf("step-update must send the changed field: %v", body["description"])
	}
	want := map[string]any{
		"title": "Server title", "stepOrder": float64(4), "featureId": "feat-existing",
		"interactionType": "output", "expectedResult": "existing result", "isCritical": true,
	}
	for k, v := range want {
		got, present := body[k]
		if !present {
			t.Errorf("step-update must resend %q; the endpoint replaces the whole row, so omitting it clears the column", k)
			continue
		}
		if got != v {
			t.Errorf("step-update must resend %q unchanged: want %v, got %v", k, v, got)
		}
	}
}

func TestJourneysStepUpdate_AppliesEveryFlag(t *testing.T) {
	var body map[string]any
	srv, _, _ := stepUpdateServer(t, fullStep(), func(b map[string]any) { body = b })
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "Open cart", "--step-order", "2", "--feature", "f1", "--description", "d", "--interaction-type", "navigation", "--expected-result", "cart shown", "--critical"); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	want := map[string]any{
		"title": "Open cart", "stepOrder": float64(2), "featureId": "f1", "description": "d",
		"interactionType": "navigation", "expectedResult": "cart shown", "isCritical": true,
	}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("step-update %q: want %v, got %v", k, v, body[k])
		}
	}
	if !strings.Contains(out.String(), `Updated step 2 "Open cart" in journey j1 (feature f1, critical true)`) {
		t.Errorf("step-update must render the server's response, got: %s", out.String())
	}
}

func TestJourneysStepUpdate_RendersResponseNotInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{fullStep()}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "s1", "step_order": 9, "title": "Title from server",
			"feature_id": "feat-server", "is_critical": false,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "Title from client"); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	if !strings.Contains(out.String(), `Updated step 9 "Title from server" in journey j1 (feature feat-server, critical false)`) {
		t.Errorf("step-update must print the server's record, not the flag values: %s", out.String())
	}
	if strings.Contains(out.String(), "Title from client") {
		t.Errorf("step-update echoed the input instead of the response: %s", out.String())
	}
}

func TestJourneysStepUpdate_ClearsCriticalWithExplicitFalse(t *testing.T) {
	var body map[string]any
	srv, _, _ := stepUpdateServer(t, fullStep(), func(b map[string]any) { body = b })
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--critical=false"); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	got, present := body["isCritical"]
	if !present {
		t.Fatalf("step-update must send isCritical when --critical=false is passed: %v", body)
	}
	if got != false {
		t.Errorf("step-update must send isCritical=false, got %v", got)
	}
}

func TestJourneysStepUpdate_JSON(t *testing.T) {
	srv, _, _ := stepUpdateServer(t, fullStep(), func(map[string]any) {})
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "T", "--json"); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("--json output must be valid JSON, got %q: %v", out.String(), err)
	}
	if got["title"] != "T" || got["id"] != "s1" {
		t.Errorf("--json must emit the server's record: %v", got)
	}
	if strings.Contains(out.String(), "Updated step") {
		t.Errorf("--json must not emit the human confirmation line: %s", out.String())
	}
}

func TestJourneysStepUpdate_RequiresAtLeastOneField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no updatable flag was passed")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1")
	if err == nil {
		t.Fatal("journeys step-update without any field flag should error")
	}
	if !strings.Contains(err.Error(), "pass at least one of") {
		t.Errorf("must fail on the missing-field guard, got: %v", err)
	}
}

func TestJourneysStepUpdate_RejectsStepOrderBelowOne(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for an out-of-range --step-order")
	}))
	defer srv.Close()

	cases := []struct{ order, want string }{
		{"0", "must be 1 or greater"},
		{"-3", "must be 1 or greater"},
		{"2147483648", "exceeds the maximum"},
	}
	for _, tc := range cases {
		f, _ := testFactory(srv)
		cmd := NewJourneysCmd(f)
		err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--step-order", tc.order)
		if err == nil {
			t.Errorf("--step-order %s should error", tc.order)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("--step-order %s must fail on the matching bound, got: %v", tc.order, err)
		}
	}
}

func TestJourneysStepUpdate_AcceptsBoundaryStepOrders(t *testing.T) {
	for _, order := range []string{"1", "2147483647"} {
		var body map[string]any
		srv, _, _ := stepUpdateServer(t, fullStep(), func(b map[string]any) { body = b })
		f, _ := testFactory(srv)
		if err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--step-order", order); err != nil {
			t.Errorf("--step-order %s is legal and must reach the server: %v", order, err)
		} else if got := fmt.Sprintf("%.0f", body["stepOrder"]); got != order {
			t.Errorf("--step-order %s must reach the wire unchanged, got %s", order, got)
		}
		srv.Close()
	}
}

func TestJourneysStepUpdate_KeepsEmptyDescriptionSemanticExplicit(t *testing.T) {
	var body map[string]any
	srv, _, _ := stepUpdateServer(t, fullStep(), func(b map[string]any) { body = b })
	defer srv.Close()

	f, _ := testFactory(srv)
	if err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--description", ""); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	got, present := body["description"]
	if !present {
		t.Fatalf("description must be sent so the full-replacement write clears it: %v", body)
	}
	if got != nil {
		t.Errorf("--description \"\" clears the column to null, consistent with --feature \"\"; got %v", got)
	}
}

func TestJourneysStepUpdate_RefusesNullRequiredField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not PUT when a required field came back null")
		}
		step := fullStep()
		step["title"] = nil
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{step}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--description", "x")
	if err == nil {
		t.Fatal("a null required field must abort rather than seed a zero value into a full replacement")
	}
	if !strings.Contains(err.Error(), "null") {
		t.Errorf("must name the null field, got: %v", err)
	}
}

func TestJourneysStepUpdate_RefusesEachMissingRequiredKey(t *testing.T) {
	for _, missing := range journeyStepRequiredResponseKeys {
		if missing == "id" {
			continue
		}
		step := fullStep()
		delete(step, missing)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("must not PUT when %q is missing from the read", missing)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{step}})
		}))
		f, _ := testFactory(srv)
		err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--description", "x")
		if err == nil {
			t.Errorf("a read missing %q must abort the write", missing)
		} else if !strings.Contains(err.Error(), missing) {
			t.Errorf("the error must name the missing key %q, got: %v", missing, err)
		}
		srv.Close()
	}
}

func TestJourneysStepUpdate_RejectsEmptyTitle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for an empty --title")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "")
	if err == nil {
		t.Fatal("an empty --title should error before any request")
	}
	if !strings.Contains(err.Error(), "--title must not be empty") {
		t.Errorf("must fail on the title guard, got: %v", err)
	}
}

func TestJourneysStepUpdate_RejectsDotSegments(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for a dot-segment id")
	}))
	defer srv.Close()

	cases := [][]string{
		{"step-update", "..", "s1", "--product", "p1", "--title", "x"},
		{"step-update", "j1", "..", "--product", "p1", "--title", "x"},
		{"step-update", "j1", "s1", "--product", ".", "--title", "x"},
	}
	for _, args := range cases {
		f, _ := testFactory(srv)
		cmd := NewJourneysCmd(f)
		err := runCmd(t, cmd, args...)
		if err == nil {
			t.Errorf("%v should error", args)
			continue
		}
		if !strings.Contains(err.Error(), "must not be") {
			t.Errorf("%v must fail on the segment guard, got: %v", args, err)
		}
	}
}

func TestJourneysStepUpdate_RejectsEmptyStepID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for an empty step id")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "", "--product", "p1", "--title", "x")
	if err == nil {
		t.Fatal("an empty step id should error before any request")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("must fail on the segment guard, got: %v", err)
	}
}

func TestJourneysStepUpdate_SurfacesUndecodableStepList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not PUT when the step list could not be decoded")
		}
		_, _ = w.Write([]byte(`{"data":["not-an-object"]}`))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "x")
	if err == nil {
		t.Fatal("an undecodable step must abort the update")
	}
	if !strings.Contains(err.Error(), "could not decode a step of journey") {
		t.Errorf("must name the decode failure, got: %v", err)
	}
}

func TestJourneysStepUpdate_ClearsFeatureWithEmptyString(t *testing.T) {
	var body map[string]any
	srv, _, _ := stepUpdateServer(t, fullStep(), func(b map[string]any) { body = b })
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--feature", ""); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	got, present := body["featureId"]
	if !present {
		t.Fatalf("featureId must be sent so the full-replacement write clears it: %v", body)
	}
	if got != nil {
		t.Errorf("--feature \"\" must send JSON null to unlink, got %v (an empty string is not a decodable UUID)", got)
	}
}

func TestJourneysStepUpdate_RefusesIncompleteServerRecord(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not PUT when the read projection is incomplete")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{
			map[string]any{"id": "s1", "step_order": 4, "title": "T"},
		}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "x")
	if err == nil {
		t.Fatal("an incomplete read projection must abort the write rather than clear the missing columns")
	}
	if !strings.Contains(err.Error(), "refusing to send a replacement") {
		t.Errorf("must name the refusal, got: %v", err)
	}
}

func TestJourneysStepUpdate_HandlesEmptyResponseBody(t *testing.T) {
	newSrv := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{fullStep()}})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
	}

	srv := newSrv()
	f, out := testFactory(srv)
	if err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--title", "T"); err != nil {
		t.Fatalf("an empty 2xx body is a successful update, not a failure: %v", err)
	}
	if !strings.Contains(out.String(), "the server returned no body") {
		t.Errorf("must confirm the update and say the body was empty: %s", out.String())
	}
	srv.Close()

	srv = newSrv()
	defer srv.Close()
	f, out = testFactory(srv)
	if err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--title", "T", "--json"); err != nil {
		t.Fatalf("--json on an empty body must also succeed: %v", err)
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Errorf("--json on an empty body must emit {}, got %q", out.String())
	}
}

func TestJourneysStepUpdate_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--title", "x")
	if err == nil {
		t.Fatal("journeys step-update without --product should error")
	}
	if !strings.Contains(err.Error(), "--product is required") {
		t.Errorf("must fail on the product guard, got: %v", err)
	}
}

func TestJourneysStepUpdate_ErrorsWhenStepNotInJourney(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not PUT when the step was not found in the journey")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{fullStep()}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "does-not-exist", "--product", "p1", "--title", "x")
	if err == nil {
		t.Fatal("journeys step-update on an unknown step should error")
	}
	if !strings.Contains(err.Error(), "not found among the 1 steps returned") {
		t.Errorf("must name the missing step and the scanned count, so a truncated list is diagnosable, got: %v", err)
	}
}

func TestJourneysStepUpdate_AcceptsCaseInsensitiveStepID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{fullStep()}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "s1", "step_order": 4, "title": "T", "feature_id": nil, "is_critical": false,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	if err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "S1", "--product", "p1", "--title", "T"); err != nil {
		t.Fatalf("the lookup matches ids case-insensitively, so the identity check must too: %v", err)
	}
	if !strings.Contains(out.String(), "Updated step 4") {
		t.Errorf("a successful update must not be reported as a failure: %s", out.String())
	}
}

func TestJourneysStepUpdate_RefusesUnknownServerField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not PUT when the read carried a field this CLI does not know")
		}
		step := fullStep()
		step["new_server_column"] = "value the CLI cannot resend"
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{step}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	err := runCmd(t, NewJourneysCmd(f), "step-update", "j1", "s1", "--product", "p1", "--description", "x")
	if err == nil {
		t.Fatal("an unknown server field must abort: the complete-record PUT would silently clear it")
	}
	if !strings.Contains(err.Error(), "new_server_column") {
		t.Errorf("must name the unknown field, got: %v", err)
	}
}

func TestJourneysStepUpdate_SurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{fullStep()}})
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "conflict", "message": "step_order already taken"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--step-order", "3")
	if err == nil {
		t.Fatal("journeys step-update must surface a server-side conflict")
	}
	if !strings.Contains(err.Error(), "step_order already taken") {
		t.Errorf("the server's message must reach the user, got: %v", err)
	}
	if strings.Contains(out.String(), "Updated step") {
		t.Errorf("a failed update must not print a success line: %s", out.String())
	}
}

func TestJourneysStepDelete(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-delete", "j1", "s1", "--product", "p1"); err != nil {
		t.Fatalf("journeys step-delete error: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/products/p1/journeys/j1/steps/s1" {
		t.Errorf("step-delete must DELETE the step path, got %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out.String(), "Deleted step s1 from journey j1") {
		t.Errorf("step-delete output missing confirmation: %s", out.String())
	}
}

func TestJourneysStepDelete_SurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "not_found", "message": "step not found"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-delete", "j1", "s1", "--product", "p1")
	if err == nil {
		t.Fatal("journeys step-delete must surface a server-side error")
	}
	if !strings.Contains(err.Error(), "step not found") {
		t.Errorf("the server's message must reach the user, got: %v", err)
	}
	if strings.Contains(out.String(), "Deleted step") {
		t.Errorf("a failed delete must not print a success line: %s", out.String())
	}
}

func TestJourneysStepDelete_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-delete", "j1", "s1")
	if err == nil {
		t.Fatal("journeys step-delete without --product should error")
	}
	if !strings.Contains(err.Error(), "--product is required") {
		t.Errorf("must fail on the product guard, got: %v", err)
	}
}

func TestJourneysStepMutations_RequireExactlyTwoArgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for a wrong positional-arg count")
	}))
	defer srv.Close()

	cases := []struct {
		name string
		args []string
	}{
		{"step-update/too-few", []string{"step-update", "j1", "--product", "p1", "--title", "x"}},
		{"step-update/too-many", []string{"step-update", "j1", "s1", "extra", "--product", "p1", "--title", "x"}},
		{"step-delete/too-few", []string{"step-delete", "j1", "--product", "p1"}},
		{"step-delete/too-many", []string{"step-delete", "j1", "s1", "extra", "--product", "p1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := testFactory(srv)
			cmd := NewJourneysCmd(f)
			err := runCmd(t, cmd, tc.args...)
			if err == nil {
				t.Fatal("should error on the positional-arg count")
			}
			if !strings.Contains(err.Error(), "accepts 2 arg") {
				t.Errorf("must reject on arg count, got: %v", err)
			}
		})
	}
}

func TestJourneysStepUpdate_RendersUnlinkedFeatureAsNone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{fullStep()}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "s1", "step_order": 1, "title": "T", "feature_id": nil, "is_critical": nil,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "T"); err != nil {
		t.Fatalf("journeys step-update error: %v", err)
	}
	if !strings.Contains(out.String(), "(feature none, critical false)") {
		t.Errorf("a step with no feature link must render as none: %s", out.String())
	}
}

func TestJourneysStepUpdate_SurfacesReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("must not PUT when the read failed")
		}
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "forbidden", "message": "no access to this journey"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	err := runCmd(t, cmd, "step-update", "j1", "s1", "--product", "p1", "--title", "x")
	if err == nil {
		t.Fatal("a failed read must abort the update")
	}
	if !strings.Contains(err.Error(), "no access to this journey") {
		t.Errorf("the read error must reach the user, got: %v", err)
	}
}

func TestJourneysStepMutations_EscapePathSegments(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewJourneysCmd(f)
	if err := runCmd(t, cmd, "step-delete", "j1", "a/b", "--product", "p1"); err != nil {
		t.Fatalf("journeys step-delete error: %v", err)
	}
	if strings.Contains(gotPath, "steps/a/b") {
		t.Errorf("a separator inside an id must not become a path separator, got %s", gotPath)
	}
	if !strings.Contains(gotPath, "a%2Fb") {
		t.Errorf("the step id must be percent-encoded, got %s", gotPath)
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
