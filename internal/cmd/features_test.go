package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFeaturesList_Table(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/features" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.URL.Query().Get("productId") != "p1" {
			t.Errorf("productId query: got %q want p1", r.URL.Query().Get("productId"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "f1", "slug": "ZEN-001", "title": "Login", "status": "planned"},
			},
			"total": 1,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "list", "--product", "p1"); err != nil {
		t.Fatalf("features list error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ZEN", "TITLE", "STATUS", "ZEN-001", "Login", "planned"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q in:\n%s", want, got)
		}
	}
}

func mixedStatusFeatures(n int) []map[string]any {
	stageOfStatus := map[string]string{"in-progress": "in_development", "testing": "in_review", "released": "shipped"}
	stagesShownAsPlanned := []string{"planned", "superseded", ""}
	items := listFixture(n)
	for i, item := range items {
		status := featureStatuses[i%len(featureStatuses)]
		item["status"] = status
		if stage, ok := stageOfStatus[status]; ok {
			item["stage"] = stage
		} else {
			item["stage"] = stagesShownAsPlanned[(i/len(featureStatuses))%len(stagesShownAsPlanned)]
		}
	}
	return items
}

func assertStatusEnvelope(t *testing.T, raw []byte, items []map[string]any, wantStatus string) {
	t.Helper()
	var env struct {
		Data    []map[string]any `json:"data"`
		Total   int              `json:"total"`
		Page    int              `json:"page"`
		PerPage int              `json:"perPage"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, raw)
	}
	want := 0
	for _, item := range items {
		if item["status"] == wantStatus {
			want++
		}
	}
	if len(env.Data) != want || env.Total != want || env.Page != 1 || env.PerPage != want {
		t.Errorf("envelope: got %d items, total %d, page %d, perPage %d; want %d items, total %d, page 1, perPage %d",
			len(env.Data), env.Total, env.Page, env.PerPage, want, want, want)
	}
	for _, item := range env.Data {
		if item["status"] != wantStatus {
			t.Errorf("%v has status %v, want only %s", item["id"], item["status"], wantStatus)
		}
	}
}

func TestFeaturesList_StatusFilterIsExactWhenTheServerDoesNotFilter(t *testing.T) {
	items := mixedStatusFeatures(150)
	tests := []struct {
		name       string
		flag       string
		wantStatus string
		wantStage  string
	}{
		{"testing narrows by stage in_review", "testing", "testing", "in_review"},
		{"in-progress narrows by stage in_development", "in-progress", "in-progress", "in_development"},
		{"planned sends no stage, because other stages also show as planned", "planned", "planned", ""},
		{"case and surrounding space are ignored", " Released ", "released", "shipped"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPagedServer(t, "/api/features", backendPages(items, 100))
			f, out := testFactory(srv.Server)
			if err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1", "--status", tc.flag, "--json"); err != nil {
				t.Fatalf("features list --status error: %v", err)
			}
			for _, q := range srv.requestedQueries() {
				if q.Has("status") {
					t.Errorf("page %s sent status=%q, which the server ignores", q.Get("page"), q.Get("status"))
				}
				if q.Get("stage") != tc.wantStage {
					t.Errorf("page %s: stage=%q, want %q", q.Get("page"), q.Get("stage"), tc.wantStage)
				}
			}
			assertStatusEnvelope(t, out.Bytes(), items, tc.wantStatus)
			assertPages(t, srv, []int{1, 2})
		})
	}
}

func TestFeaturesList_StatusFilterLetsTheServerNarrowThePages(t *testing.T) {
	items := mixedStatusFeatures(150)
	tests := []struct {
		flag      string
		wantPages string
	}{
		{"testing", "1"},
		{"in-progress", "1"},
		{"released", "1"},
		{"planned", "1,2"},
	}
	for _, tc := range tests {
		t.Run(tc.flag, func(t *testing.T) {
			var gotPages []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				matching := items
				if stage := q.Get("stage"); stage != "" {
					matching = nil
					for _, item := range items {
						if item["stage"] == stage {
							matching = append(matching, item)
						}
					}
				}
				gotPages = append(gotPages, q.Get("page"))
				page, perPage := 0, 0
				_, _ = fmt.Sscan(q.Get("page"), &page)
				_, _ = fmt.Sscan(q.Get("per_page"), &perPage)
				_, body := backendPages(matching, 100)(page, perPage)
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer srv.Close()

			f, out := testFactory(srv)
			if err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1", "--status", tc.flag, "--json"); err != nil {
				t.Fatalf("features list --status error: %v", err)
			}
			assertStatusEnvelope(t, out.Bytes(), items, tc.flag)
			if got := strings.Join(gotPages, ","); got != tc.wantPages {
				t.Errorf("requested pages %s, want %s", got, tc.wantPages)
			}
		})
	}
}

func TestFeaturesList_StatusFilterInTable(t *testing.T) {
	items := mixedStatusFeatures(150)
	srv := newPagedServer(t, "/api/features", backendPages(items, 100))
	f, out := testFactory(srv.Server)
	if err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1", "--status", "in-progress"); err != nil {
		t.Fatalf("features list --status error: %v", err)
	}
	got := out.String()
	for _, item := range items {
		slug := item["slug"].(string)
		if shown := strings.Contains(got, slug); shown != (item["status"] == "in-progress") {
			t.Errorf("%s (status %v): shown %t", slug, item["status"], shown)
		}
	}
}

func TestFeaturesList_RejectsUnknownStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for an unknown --status")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1", "--status", "in_review")
	if err == nil || !strings.Contains(err.Error(), "--status must be one of planned, in-progress, testing, released") {
		t.Fatalf("an unknown status must be rejected with the valid values, got: %v", err)
	}
}

func TestFeaturesList_StatusFilterNeedsAListResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"unexpected":true}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	err := runCmd(t, NewFeaturesCmd(f), "list", "--product", "p1", "--status", "testing", "--json")
	if err == nil || !strings.Contains(err.Error(), "cannot apply --status") {
		t.Fatalf("a response that is not a list cannot be filtered and must fail, got: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("an unfiltered body must not be printed as if it were filtered, got:\n%s", out.String())
	}
}

func TestFeaturesList_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called without --product")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "list"); err == nil {
		t.Fatal("features list without --product should error")
	}
}

func TestFeaturesGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/features/f1" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "slug": "ZEN-001", "title": "Login"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "get", "f1"); err != nil {
		t.Fatalf("features get error: %v", err)
	}
	if !strings.Contains(out.String(), "ZEN-001") {
		t.Errorf("get output missing zen id: %s", out.String())
	}
}

func TestFeaturesCreate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f9", "slug": "my-feature", "title": "Login"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--component", "c1", "--title", "Login", "--slug", "my-feature", "--status", "planned"); err != nil {
		t.Fatalf("features create error: %v", err)
	}
	if body["productId"] != "p1" || body["componentId"] != "c1" || body["slug"] != "my-feature" || body["title"] != "Login" || body["status"] != "planned" {
		t.Errorf("create body must carry productId, componentId, slug, title, status: %v", body)
	}
	if !strings.Contains(out.String(), "f9") && !strings.Contains(out.String(), "my-feature") {
		t.Errorf("create output missing created feature: %s", out.String())
	}
}

func TestFeaturesCreate_DerivesSlugFromTitle(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f9"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--component", "c1", "--title", "User Login Flow!"); err != nil {
		t.Fatalf("features create error: %v", err)
	}
	if body["slug"] != "user-login-flow" {
		t.Errorf("slug should be derived from title: got %v want user-login-flow", body["slug"])
	}
}

func TestFeaturesCreate_RequiresComponent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --component is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "create", "--product", "p1", "--title", "Login"); err == nil {
		t.Fatal("features create without --component should error (backend requires componentId for top-level features)")
	}
}

func TestFeaturesStatus(t *testing.T) {
	var gotStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/features/f1/status" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotStatus, _ = body["status"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "status": "testing"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "status", "f1", "testing"); err != nil {
		t.Fatalf("features status error: %v", err)
	}
	if gotStatus != "testing" {
		t.Errorf("status body: got %q want testing", gotStatus)
	}
	if !strings.Contains(strings.ToLower(out.String()), "testing") {
		t.Errorf("status output should confirm new status: %s", out.String())
	}
}

func TestFeaturesUpdate(t *testing.T) {
	var gotGet bool
	var patch map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/features/f1":
			gotGet = true
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "slug": "zen-001", "title": "Old"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/features/f1":
			_ = json.NewDecoder(r.Body).Decode(&patch)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "title": "NewTitle"})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "update", "f1", "--title", "NewTitle"); err != nil {
		t.Fatalf("features update error: %v", err)
	}
	if !gotGet {
		t.Error("update must GET the current feature to obtain its slug (backend PATCH requires slug+title)")
	}
	if patch["slug"] != "zen-001" {
		t.Errorf("update must resend the current slug: got %v want zen-001", patch["slug"])
	}
	if patch["title"] != "NewTitle" {
		t.Errorf("update title: got %v want NewTitle", patch["title"])
	}
}

func TestFeaturesUpdate_RequiresAField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no update flags are passed")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "update", "f1"); err == nil {
		t.Fatal("features update with no flags should error")
	}
}

func TestFeaturesUpdate_RejectsEmptyTitle(t *testing.T) {
	patched := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "slug": "zen-001", "title": "Old"})
			return
		}
		if r.Method == http.MethodPatch {
			patched = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "update", "f1", "--title", ""); err == nil {
		t.Fatal("features update with an empty --title should error before sending a PATCH")
	}
	if patched {
		t.Error("must not PATCH when the resulting title would be empty (backend requires a non-empty title)")
	}
}

func TestFeaturesHistory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/history" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"featureId": "f1",
			"entries":   []map[string]any{{"type": "status_change", "timestamp": "2024-01-01T00:00:00Z"}},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "history", "f1"); err != nil {
		t.Fatalf("features history error: %v", err)
	}
	if !strings.Contains(out.String(), "status_change") {
		t.Errorf("history output missing entries: %s", out.String())
	}
}

func TestFeaturesDeprecate(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/lifecycle/deprecate" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "status": "deprecated"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "deprecate", "f1", "--reason", "obsolete", "--replacement", "f2", "--removal-planned-at", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatalf("features deprecate error: %v", err)
	}
	if body["reason"] != "obsolete" || body["replacementId"] != "f2" || body["removalPlannedAt"] != "2025-01-01T00:00:00Z" {
		t.Errorf("deprecate body must carry reason, replacementId, removalPlannedAt: %v", body)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("deprecate output missing feature id: %s", out.String())
	}
}

func TestFeaturesSplit(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/lifecycle/split" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"sourceFeature": map[string]any{"id": "f1"}, "children": []map[string]any{{"id": "c1"}}})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "split", "f1", "--children", `[{"title":"Child A","slug":"child-a"}]`, "--reason", "too big"); err != nil {
		t.Fatalf("features split error: %v", err)
	}
	if body["reason"] != "too big" {
		t.Errorf("split body must carry reason: %v", body)
	}
	kids, ok := body["children"].([]any)
	if !ok || len(kids) != 1 {
		t.Fatalf("split body must carry a children array: %v", body["children"])
	}
	first, _ := kids[0].(map[string]any)
	if first["title"] != "Child A" || first["slug"] != "child-a" {
		t.Errorf("split child must carry title and slug: %v", first)
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("split output missing feature id: %s", out.String())
	}
}

func TestFeaturesSplit_RequiresChildren(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --children is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "split", "f1"); err == nil {
		t.Fatal("features split without --children should error")
	}
}

func TestFeaturesMerge(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/lifecycle/merge" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1", "slug": "merged"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "merge", "f1", "--source", `["f2","f3"]`, "--title", "Merged", "--slug", "merged"); err != nil {
		t.Fatalf("features merge error: %v", err)
	}
	if body["title"] != "Merged" || body["slug"] != "merged" {
		t.Errorf("merge body must carry title and slug: %v", body)
	}
	ids, ok := body["sourceIds"].([]any)
	if !ok || len(ids) != 2 || ids[0] != "f2" || ids[1] != "f3" {
		t.Errorf("merge body must carry sourceIds array: %v", body["sourceIds"])
	}
	if !strings.Contains(out.String(), "merged") {
		t.Errorf("merge output missing merged slug: %s", out.String())
	}
}

func TestFeaturesMerge_RequiresSourceTitleSlug(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when required merge flags are missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "merge", "f1", "--source", `["f2"]`); err == nil {
		t.Fatal("features merge without --title and --slug should error")
	}
}

func TestFeaturesRevision(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/revisions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r1", "version": "v2"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "revision", "f1", "--scope-summary", "expand scope", "--coverage-target", "80", "--docs-required"); err != nil {
		t.Fatalf("features revision error: %v", err)
	}
	if body["scopeSummary"] != "expand scope" {
		t.Errorf("revision body must carry scopeSummary: %v", body)
	}
	if body["coverageTarget"] != float64(80) {
		t.Errorf("revision body must carry coverageTarget when set: %v", body["coverageTarget"])
	}
	if body["docsRequired"] != true {
		t.Errorf("revision body must carry docsRequired when set: %v", body["docsRequired"])
	}
	if !strings.Contains(out.String(), "v2") {
		t.Errorf("revision output missing version: %s", out.String())
	}
}

func TestFeaturesRevision_RequiresScopeSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --scope-summary is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewFeaturesCmd(f)
	if err := runCmd(t, cmd, "revision", "f1"); err == nil {
		t.Fatal("features revision without --scope-summary should error")
	}
}

func TestSlugifyCapsAtBackendLimit(t *testing.T) {
	got := slugify(strings.Repeat("ab ", 120))
	if got == "" {
		t.Fatal("slugify should produce a non-empty slug")
	}
	if len(got) > 200 {
		t.Errorf("slug must be capped at 200 chars (backend limit), got %d", len(got))
	}
	if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Errorf("slug must not start or end with a dash, got %q", got)
	}
}
