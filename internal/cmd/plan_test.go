package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	plProduct   = "11111111-2222-4333-8444-555555555555"
	plComponent = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
	plFeature42 = "aaaaaaaa-0000-4000-8000-000000000042"
	plFeature7  = "aaaaaaaa-0000-4000-8000-000000000007"
	plFeatureR  = "aaaaaaaa-0000-4000-8000-0000000000a1"
	plNewFeat   = "aaaaaaaa-0000-4000-8000-00000000000f"
	plRevision  = "bbbbbbbb-0000-4000-8000-000000000001"
	plRev42     = "bbbbbbbb-0000-4000-8000-000000000042"
	plRev7      = "bbbbbbbb-0000-4000-8000-000000000007"
	plPlan      = "cccccccc-0000-4000-8000-000000000001"
	plTask      = "66666666-7777-4888-8999-000000000000"
	plRepo      = "github.com/acme/app"
)

func loginFactory(srv *httptest.Server) (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	f := &Factory{Out: out, NewClient: func(context.Context) (*client.Client, error) {
		return client.New(&config.Config{AccessToken: "browser-login"}, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client())), nil
	}}
	return f, out
}

func sessionFactory(srv *httptest.Server) (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	f := &Factory{Out: out, NewClient: func(context.Context) (*client.Client, error) {
		return client.New(&config.Config{AccessToken: client.SessionTokenPrefix + "abc"}, srv.URL, "", client.WithHTTPClient(srv.Client())), nil
	}}
	return f, out
}

func writePlanFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
	return path
}

func hashOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func featureRow(id, slug, stage, revision string) map[string]any {
	row := map[string]any{"id": id, "product_id": plProduct, "component_id": plComponent, "parent_feature_id": nil, "active_revision_id": nil, "slug": slug, "title": "Feature " + slug, "stage": stage}
	if revision != "" {
		row["active_revision_id"] = revision
	}
	return row
}

func featureUsage(maxFeatures, features int) map[string]any {
	return map[string]any{"limits": map[string]any{"maxFeatures": maxFeatures}, "usage": map[string]any{"features": features}}
}

func featureList(rows ...map[string]any) map[string]any {
	data := make([]any, 0, len(rows))
	for _, r := range rows {
		data = append(data, r)
	}
	return map[string]any{"data": data, "total": len(rows), "perPage": 100}
}

func planPushRoutes(extra map[string]func(recordedRequest) fakeReply) map[string]func(recordedRequest) fakeReply {
	routes := map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans":   reply(200, map[string]any{"data": []any{}, "total": 0, "perPage": 100}),
		"GET /api/products/" + plProduct + "/repositories": reply(200, map[string]any{"data": []any{map[string]any{"id": "r0", "repository": "gitlab.com/acme/other"}, map[string]any{"id": "r1", "repository": plRepo}}}),
		"GET /api/tasks/" + plTask:                         reply(200, map[string]any{"id": plTask, "title": "Checkout"}),
		"GET /api/billing/usage":                           reply(200, featureUsage(-1, 3)),
		"GET /api/products/" + plProduct + "/components":   reply(200, map[string]any{"data": []any{map[string]any{"id": "77777777-8888-4999-8aaa-cccccccccccc", "product_id": plProduct, "name": "Billing"}, map[string]any{"id": plComponent, "product_id": plProduct, "name": "Checkout"}}, "total": 2, "perPage": 100}),
		"GET /api/features/resolve": func(r recordedRequest) fakeReply {
			switch r.Query.Get("ref") {
			case "ZEN-42":
				return fakeReply{Body: featureRow(plFeature42, "zen-42", "in_development", plRev42)}
			case "ZEN-7":
				return fakeReply{Body: featureRow(plFeature7, "zen-7", "shipped", plRev7)}
			}
			return fakeReply{Status: 404, Body: `{"code":"not_found","message":"no feature"}`}
		},
		"GET /api/features":  reply(200, featureList(map[string]any{"id": "other", "slug": "unrelated"}, featureRow(plFeatureR, "refund-export", "planned", plRevision))),
		"POST /api/features": reply(201, map[string]any{"id": plNewFeat}),
		"POST /api/features/" + plFeature7 + "/revisions": reply(201, map[string]any{"id": plRevision, "version": "v2"}),
		"POST /api/products/" + plProduct + "/work-plans": reply(201, map[string]any{"id": plPlan, "name": "Checkout hardening", "status": "draft", "integration_branch": "plan/checkout", "base_branch": "develop", "created": true, "orders": []any{map[string]any{"id": "o1", "status": "draft", "attempt": 0}, map[string]any{"id": "o2", "status": "draft"}, map[string]any{"id": "o3", "status": "draft"}}}),
	}
	for k, v := range extra {
		routes[k] = v
	}
	return routes
}

func assertCalls(t *testing.T, api *fakeAPI, want ...string) {
	t.Helper()
	got := api.summary()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestPlanPush_CreatesMissingFeaturesRevisionsAndThePlan(t *testing.T) {
	path := writePlanFile(t, validPlanFile)
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", path); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	assertCalls(t, api,
		"GET /api/products/"+plProduct+"/work-plans",
		"GET /api/products/"+plProduct+"/repositories",
		"GET /api/features/resolve",
		"GET /api/features/resolve",
		"GET /api/features",
		"GET /api/products/"+plProduct+"/components",
		"GET /api/billing/usage",
		"POST /api/features/"+plFeature7+"/revisions",
		"POST /api/features",
		"POST /api/products/"+plProduct+"/work-plans",
	)
	rev := api.only(t, "POST", "/api/features/"+plFeature7+"/revisions").Body
	if rev["scopeSummary"] != "Refund partial captures" {
		t.Fatalf("revision body = %v", rev)
	}
	if ac := rev["acceptanceCriteria"].([]any); len(ac) != 1 || ac[0] != "FR-001: Partial captures refund proportionally" {
		t.Fatalf("acceptance criteria = %v", rev["acceptanceCriteria"])
	}
	feat := api.only(t, "POST", "/api/features").Body
	if feat["productId"] != plProduct || feat["componentId"] != plComponent || feat["slug"] != "refund-webhook" || feat["title"] != "Refund webhook" || feat["description"] != "Accept refund webhooks\n\nAcceptance criteria:\n- IF-001: POST /webhooks/refund accepts a signed payload" {
		t.Fatalf("feature body = %v", feat)
	}
	if q := api.only(t, "GET", "/api/features").Query; q.Get("productId") != plProduct || q.Get("per_page") != "100" || q.Get("page") != "1" {
		t.Fatalf("feature list query = %v", q)
	}
	plan := api.only(t, "POST", "/api/products/"+plProduct+"/work-plans").Body
	want := map[string]any{"name": "Checkout hardening", "repository": "https://github.com/acme/app", "sourceKind": "plan_file", "contentHash": hashOf(validPlanFile), "createdVia": "cli", "baseBranch": "develop", "integrationBranch": "plan/checkout", "preferredHarness": "claude"}
	for k, v := range want {
		if plan[k] != v {
			t.Errorf("plan[%s] = %v, want %v", k, plan[k], v)
		}
	}
	if _, ok := plan["taskId"]; ok {
		t.Errorf("a plan file without task must not send taskId: %v", plan["taskId"])
	}
	if plan["sourcePath"] != "plan.md" {
		t.Errorf("sourcePath = %v, want the base name of a path outside the working directory and any git repository", plan["sourcePath"])
	}
	items := plan["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items = %v", items)
	}
	i0, i1, i2 := items[0].(map[string]any), items[1].(map[string]any), items[2].(map[string]any)
	if i0["featureId"] != plFeature42 || i0["featureRevisionId"] != nil || i0["paths"].([]any)[0] != "backend/internal/billing/refunds.go" {
		t.Fatalf("item 0 = %v", i0)
	}
	reqs := i0["requirements"].([]any)
	if len(reqs) != 2 || reqs[1].(map[string]any)["id"] != "AC-002" || len(reqs[1].(map[string]any)["tags"].([]any)) != 2 || len(reqs[0].(map[string]any)["tags"].([]any)) != 0 {
		t.Fatalf("item 0 requirements = %v", reqs)
	}
	if i1["featureRevisionId"] != plRevision || i1["featureId"] != nil || len(i1["paths"].([]any)) != 0 {
		t.Fatalf("item 1 = %v", i1)
	}
	if i2["featureId"] != plNewFeat || i2["featureRevisionId"] != nil {
		t.Fatalf("item 2 = %v", i2)
	}
	lines := strings.Split(out.String(), "\n")
	wantLines := []string{
		"item 1: ZEN-42 already exists, so the texts of AC-001, AC-002 are ignored; the plan records only requirement IDs and tags",
		"item 2: created revision v2 of ZEN-7",
		`item 3: created feature "Refund webhook" (` + plNewFeat + `)`,
		"Pushed plan " + plPlan + " with 3 work order(s).",
		`Plan ` + plPlan + ` "Checkout hardening": draft (plan/checkout → develop)`,
	}
	if len(lines) < len(wantLines)+1 || strings.Join(lines[:len(wantLines)], "\n") != strings.Join(wantLines, "\n") {
		t.Fatalf("output:\n%s\nwant it to start with:\n%s", out.String(), strings.Join(wantLines, "\n"))
	}
	if !strings.HasPrefix(lines[len(wantLines)], "ORDER  STATUS  ATTEMPT  PR") {
		t.Fatalf("orders table = %q", lines[len(wantLines)])
	}
}

func assertNoWrites(t *testing.T, api *fakeAPI) {
	t.Helper()
	for _, call := range api.summary() {
		if !strings.HasPrefix(call, "GET ") {
			t.Fatalf("the push must stop before any write: %v", api.summary())
		}
	}
}

const plHead = "---\nproduct: " + plProduct + "\nrepository: https://github.com/acme/app\nname: P\nitems:\n"

func TestPlanPush_LooksUpEveryItemBeforeItWrites(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, _ := loginFactory(srv)
	content := plHead + "  - title: Refund webhook\n    component: " + plComponent + "\n  - feature: ZEN-7\n    new_revision: true\n    scope_summary: S\n  - feature: ZEN-99\n---\n"
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, content))
	if err == nil || err.Error() != "item 3: feature ZEN-99: no feature (status 404)" {
		t.Fatalf("error = %v", err)
	}
	assertNoWrites(t, api)
	assertCalls(t, api,
		"GET /api/products/"+plProduct+"/work-plans",
		"GET /api/products/"+plProduct+"/repositories",
		"GET /api/features",
		"GET /api/products/"+plProduct+"/components",
		"GET /api/features/resolve",
		"GET /api/features/resolve",
	)
}

func TestPlanPush_RefusesTwoItemsOfOneFeature(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/features": reply(200, featureList(featureRow(plFeature42, "refund-webhook", "in_development", plRev42))),
	}))
	f, _ := loginFactory(srv)
	content := plHead + "  - feature: ZEN-42\n  - title: Refund webhook\n    component: " + plComponent + "\n---\n"
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, content))
	if err == nil || err.Error() != "items 1 and 2 plan the same feature "+plFeature42 {
		t.Fatalf("error = %v", err)
	}
	assertNoWrites(t, api)
}

func TestPlanPush_ReportsWhatItCreatedWhenThePlanFails(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"POST /api/products/" + plProduct + "/work-plans": reply(422, `{"code":"duplicate_item","message":"item 1 repeats a revision of the plan"}`),
	}))
	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	want := `item 1 repeats a revision of the plan (status 422); created before the failure: revision v2 of ZEN-7, feature "Refund webhook" (` + plNewFeat + `); pushing the plan again reuses them`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v", err)
	}
	if api.count("POST", "/api/features") != 1 || api.count("POST", "/api/features/"+plFeature7+"/revisions") != 1 {
		t.Fatalf("calls = %v", api.summary())
	}
}

func TestPlanPush_ReportsEarlierWritesWhenALaterWriteFails(t *testing.T) {
	srv, _ := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"POST /api/features": reply(409, `{"code":"conflict","message":"feature with this slug already exists"}`),
	}))
	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	want := `item 3: create feature "Refund webhook": feature with this slug already exists (status 409); created before the failure: revision v2 of ZEN-7; pushing the plan again reuses them`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanPush_RefusesAPlanFileNameZensuCannotRecord(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, _ := loginFactory(srv)
	path := filepath.Join(t.TempDir(), "plan#1.md")
	if err := os.WriteFile(path, []byte(validPlanFile), 0o600); err != nil {
		t.Fatalf("write plan file: %v", err)
	}
	err := runCmd(t, NewPlanCmd(f), "push", path, "--dry-run")
	if err == nil || !strings.Contains(err.Error(), `rename the plan file; Zensu records its path "plan#1.md"`) {
		t.Fatalf("error = %v", err)
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("calls = %v", api.summary())
	}
}

func TestPlanPush_IsIdempotentOnTheContentHash(t *testing.T) {
	path := writePlanFile(t, validPlanFile)
	listed := map[string]any{"data": []any{
		map[string]any{"id": "old", "status": "merged", "source_ref": map[string]any{"content_hash": hashOf(validPlanFile)}},
		map[string]any{"id": plPlan, "status": "open", "source_ref": map[string]any{"content_hash": hashOf(validPlanFile)}},
	}, "total": 2, "perPage": 100}
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": reply(200, listed),
		"GET /api/work-plans/" + plPlan:                  reply(200, planDetail("open", map[string]any{"id": "o1", "status": "queued", "attempt": 0})),
	}))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", path); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	assertCalls(t, api, "GET /api/products/"+plProduct+"/work-plans")
	if out.String() != "Already pushed: plan "+plPlan+" (open) holds this file's content; nothing was created.\n" {
		t.Fatalf("output = %q", out.String())
	}
	f2, out2 := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "push", path, "--json"); err != nil {
		t.Fatalf("plan push json: %v", err)
	}
	assertCalls(t, api, "GET /api/products/"+plProduct+"/work-plans", "GET /api/products/"+plProduct+"/work-plans", "GET /api/work-plans/"+plPlan)
	env, keys := decodePushResult(t, out2.String())
	if strings.Join(keys, ",") != "already_pushed,dry_run,notes,plan" || env.DryRun || !env.AlreadyPushed || len(env.Notes) != 0 || env.Notes == nil {
		t.Fatalf("envelope = %+v (keys %v)", env, keys)
	}
	if env.Plan["id"] != plPlan || env.Plan["status"] != "open" || len(env.Plan["orders"].([]any)) != 1 {
		t.Fatalf("plan = %v", env.Plan)
	}
}

func TestPlanPush_PagesThroughPlansAndSlugs(t *testing.T) {
	path := writePlanFile(t, validPlanFile)
	var planPages, featurePages int32
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": func(r recordedRequest) fakeReply {
			atomic.AddInt32(&planPages, 1)
			if r.Query.Get("page") == "1" {
				return fakeReply{Body: map[string]any{"data": []any{map[string]any{"id": "x", "status": "open", "source_ref": map[string]any{"content_hash": "other"}}}, "total": 2, "perPage": 1}}
			}
			return fakeReply{Body: map[string]any{"data": []any{map[string]any{"id": "y", "status": "abandoned", "source_ref": map[string]any{"content_hash": hashOf(validPlanFile)}}}, "total": 2, "perPage": 1}}
		},
		"GET /api/features": func(r recordedRequest) fakeReply {
			atomic.AddInt32(&featurePages, 1)
			if r.Query.Get("page") == "1" {
				return fakeReply{Body: map[string]any{"data": []any{map[string]any{"id": "f-other", "slug": "other"}}, "total": 2, "perPage": 1}}
			}
			return fakeReply{Body: map[string]any{"data": []any{featureRow(plNewFeat, "refund-webhook", "planned", "bbbbbbbb-0000-4000-8000-00000000000f")}, "total": 2, "perPage": 1}}
		},
		"GET /api/work-plans/x": reply(200, map[string]any{"id": "x", "status": "open", "items": []any{map[string]any{"feature_revision_id": plRev42, "closed_at": "2026-09-20T10:00:00Z"}}}),
	}))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", path); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	if planPages != 2 || featurePages != 2 {
		t.Fatalf("pages read: plans %d features %d, want 2 and 2", planPages, featurePages)
	}
	if api.count("GET", "/api/work-plans/x") != 1 || api.count("GET", "/api/work-plans/y") != 0 {
		t.Fatalf("live plan reads = %v, want only the live plan x", api.summary())
	}
	if api.count("POST", "/api/features") != 0 {
		t.Fatal("a feature found by slug must be reused")
	}
	for _, want := range []string{
		"item 3: reusing feature refund-webhook (" + plNewFeat + ")\n",
		"item 3: feature refund-webhook already exists, so the texts of IF-001 are ignored; the plan records only requirement IDs and tags\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output misses %q:\n%s", want, out.String())
		}
	}
	item := api.only(t, "POST", "/api/products/"+plProduct+"/work-plans").Body["items"].([]any)[2].(map[string]any)
	if item["featureId"] != plNewFeat {
		t.Fatalf("item = %v", item)
	}
}

func TestPlanPush_ReusesAnOpenRevisionOfAShippedFeature(t *testing.T) {
	file := plHead + "  - feature: ZEN-42\n    new_revision: true\n    scope_summary: S\n    requirements: [{id: AC-001, text: T}]\n---\n"
	path := writePlanFile(t, file)
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", path); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	if api.count("POST", "/api/features/"+plFeature42+"/revisions") != 0 {
		t.Fatal("an open revision must not be recreated")
	}
	item := api.only(t, "POST", "/api/products/"+plProduct+"/work-plans").Body["items"].([]any)[0].(map[string]any)
	if item["featureId"] != plFeature42 || item["featureRevisionId"] != nil {
		t.Fatalf("item = %v", item)
	}
	want := "item 1: ZEN-42 already has an open revision (in_development); the plan targets it\n" +
		"item 1: the open revision of ZEN-42 already exists, so the texts of AC-001 are ignored; the plan records only requirement IDs and tags\n" +
		"Pushed plan " + plPlan + " with 3 work order(s).\n"
	if !strings.HasPrefix(out.String(), want) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestPlanPush_RefusesAShippedFeatureWithoutNewRevision(t *testing.T) {
	file := plHead + "  - feature: ZEN-7\n---\n"
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file))
	if err == nil || err.Error() != "item 1: the active revision of ZEN-7 is shipped; set new_revision with a scope_summary to plan a new revision" {
		t.Fatalf("error = %v", err)
	}
	assertNoWrites(t, api)
}

func TestPlanPush_RefusesAFeatureOfAnotherProduct(t *testing.T) {
	file := plHead + "  - feature: " + plFeature42 + "\n---\n"
	other := featureRow(plFeature42, "zen-42", "in_development", plRev42)
	other["product_id"] = "99999999-0000-4000-8000-000000000000"
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/features/" + plFeature42: reply(200, other),
	}))
	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file))
	if err == nil || err.Error() != "item 1: feature "+plFeature42+" belongs to another product" {
		t.Fatalf("error = %v", err)
	}
	assertNoWrites(t, api)
}

func TestPlanPush_DryRunCreatesNothing(t *testing.T) {
	path := writePlanFile(t, validPlanFile)
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", path, "--dry-run"); err != nil {
		t.Fatalf("plan push --dry-run: %v", err)
	}
	assertNoWrites(t, api)
	wantStart := "item 1: ZEN-42 already exists, so the texts of AC-001, AC-002 are ignored; the plan records only requirement IDs and tags\n" +
		"item 2: would create a new revision of ZEN-7\n" +
		"item 3: would create feature \"Refund webhook\" (refund-webhook)\n{\n"
	if !strings.HasPrefix(out.String(), wantStart) {
		t.Fatalf("dry run output:\n%s", out.String())
	}
	for _, want := range []string{`"sourceKind": "plan_file"`, `"contentHash": "` + hashOf(validPlanFile) + `"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("dry run output misses %q:\n%s", want, out.String())
		}
	}
}

func TestPlanPush_JSONOutputAndProductFlag(t *testing.T) {
	file := "---\nrepository: https://github.com/acme/app\nname: P\nitems:\n  - revision: " + plRevision + "\n---\n"
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file), "--product", plProduct, "--json"); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	item := api.only(t, "POST", "/api/products/"+plProduct+"/work-plans").Body["items"].([]any)[0].(map[string]any)
	if item["featureRevisionId"] != plRevision || item["featureId"] != nil {
		t.Fatalf("item = %v", item)
	}
	env, keys := decodePushResult(t, out.String())
	if strings.Join(keys, ",") != "already_pushed,dry_run,notes,plan" || env.DryRun || env.AlreadyPushed || len(env.Notes) != 0 {
		t.Fatalf("envelope = %+v (keys %v)", env, keys)
	}
	if env.Plan["id"] != plPlan || env.Plan["created"] != true || len(env.Plan["orders"].([]any)) != 3 {
		t.Fatalf("plan = %v", env.Plan)
	}
}

func TestPlanPush_AlreadyPushedAnswerFromTheServer(t *testing.T) {
	file := plHead + "  - revision: " + plRevision + "\n---\n"
	srv, _ := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"POST /api/products/" + plProduct + "/work-plans": reply(200, map[string]any{"id": plPlan, "status": "open", "created": false, "orders": []any{}}),
	}))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file)); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	if !strings.Contains(out.String(), "Already pushed plan "+plPlan+" with 0 work order(s).") {
		t.Fatalf("output = %s", out.String())
	}
	f2, out2 := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, file), "--json"); err != nil {
		t.Fatalf("plan push json: %v", err)
	}
	env, keys := decodePushResult(t, out2.String())
	if strings.Join(keys, ",") != "already_pushed,dry_run,notes,plan" || env.DryRun || !env.AlreadyPushed || env.Plan["id"] != plPlan || env.Plan["created"] != false {
		t.Fatalf("envelope = %+v (keys %v)", env, keys)
	}
}

func TestPlanPush_Errors(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"POST /api/features/" + plFeature7 + "/revisions": reply(400, `{"code":"bad_request","message":"previous revision must be released or superseded before creating a new one"}`),
	}))
	f, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("a missing file must fail")
	}
	f2, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, "# no front matter\n")); err == nil || !strings.Contains(err.Error(), "front-matter") {
		t.Fatalf("error = %v", err)
	}
	f3, _ := loginFactory(srv)
	noProduct := "---\nrepository: " + plRepo + "\nname: P\nitems:\n  - feature: ZEN-42\n---\n"
	if err := runCmd(t, NewPlanCmd(f3), "push", writePlanFile(t, noProduct)); err == nil || err.Error() != "the product must be a product UUID (front matter product or --product)" {
		t.Fatalf("error = %v", err)
	}
	f5, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f5), "push", writePlanFile(t, noProduct), "--product", "ZEN"); err == nil || err.Error() != `--product must be a UUID, got "ZEN"` {
		t.Fatalf("error = %v", err)
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("invalid input sent requests: %v", api.summary())
	}
	f4, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f4), "push", writePlanFile(t, validPlanFile)); err == nil || err.Error() != "item 2: create a revision of ZEN-7: previous revision must be released or superseded before creating a new one (status 400)" {
		t.Fatalf("error = %v", err)
	}
	if api.count("POST", "/api/products/"+plProduct+"/work-plans") != 0 {
		t.Fatal("no plan after a failed revision")
	}
}

func TestPlanPush_UnexpectedResponses(t *testing.T) {
	cases := map[string]map[string]func(recordedRequest) fakeReply{
		"invalid list response":                {"GET /api/products/" + plProduct + "/work-plans": reply(200, `[]`)},
		"invalid repository list":              {"GET /api/products/" + plProduct + "/repositories": reply(200, `[]`)},
		"repositories of product " + plProduct: {"GET /api/products/" + plProduct + "/repositories": reply(503, `{"code":"unavailable","message":"down"}`)},
		"feature ZEN-42: unexpected response":  {"GET /api/features/resolve": reply(200, `{}`)},
		"item 2: unexpected revision response": {"POST /api/features/" + plFeature7 + "/revisions": reply(201, `{}`)},
		"item 3: unexpected feature response":  {"POST /api/features": reply(201, `{}`)},
		"invalid work plan response":           {"POST /api/products/" + plProduct + "/work-plans": reply(201, `[]`)},
		"no feature":                           {"GET /api/features/resolve": reply(404, `{"code":"not_found","message":"no feature"}`)},
		"create feature":                       {"POST /api/features": reply(409, `{"code":"conflict","message":"feature with this slug already exists"}`)},
		"item 3: invalid list response":        {"GET /api/features": reply(200, `[]`)},
		"item 3: invalid feature list":         {"GET /api/features": reply(200, `{"data":[1],"total":1,"perPage":100}`)},
		"item 3: invalid component list":       {"GET /api/products/" + plProduct + "/components": reply(200, `{"data":[1],"total":1,"perPage":100}`)},
	}
	for want, extra := range cases {
		t.Run(want, func(t *testing.T) {
			srv, _ := newFakeAPI(t, planPushRoutes(extra))
			f, _ := loginFactory(srv)
			err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
		})
	}
}

func TestPlanSourcePath(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		".zensu/plans/x.md":                        ".zensu/plans/x.md",
		"./.zensu/plans/x.md":                      ".zensu/plans/x.md",
		"../elsewhere/y.md":                        "y.md",
		filepath.Join(wd, "plans", "z.md"):         "plans/z.md",
		filepath.Join(os.TempDir(), "abs", "a.md"): "a.md",
	}
	for in, want := range cases {
		if got := planSourcePath(in); got != want {
			t.Errorf("planSourcePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func planDetail(status string, orders ...map[string]any) map[string]any {
	list := make([]any, 0, len(orders))
	for _, o := range orders {
		list = append(list, o)
	}
	return map[string]any{"id": plPlan, "name": "Checkout", "status": status, "base_branch": "main", "integration_branch": "plan/checkout", "orders": list}
}

func TestPlanStatus_OnceAndWatch(t *testing.T) {
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: func(recordedRequest) fakeReply {
			n := atomic.AddInt32(&polls, 1)
			switch {
			case n <= 2:
				return fakeReply{Body: planDetail("open", map[string]any{"id": "o1", "status": "implementing", "attempt": 1})}
			default:
				pr := "https://github.com/acme/app/pull/3"
				body := planDetail("merged", map[string]any{"id": "o1", "status": "merged", "attempt": 1, "pr_url": pr})
				body["final_pr_url"] = pr
				return fakeReply{Body: body}
			}
		},
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "status", plPlan); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), `Plan `+plPlan+` "Checkout": open (plan/checkout → main)`) || !strings.Contains(out.String(), "implementing") {
		t.Fatalf("status output = %s", out.String())
	}
	f2, out2 := testFactory(srv)
	start := time.Now()
	if err := runCmd(t, NewPlanCmd(f2), "status", plPlan, "--watch", "--interval", "1s"); err != nil {
		t.Fatalf("status --watch: %v", err)
	}
	if polls != 3 {
		t.Fatalf("polls = %d, want 3", polls)
	}
	if strings.Count(out2.String(), "Plan "+plPlan) != 2 {
		t.Fatalf("watch must print only changes:\n%s", out2.String())
	}
	if !strings.Contains(out2.String(), "merged") || !strings.Contains(out2.String(), "final PR https://github.com/acme/app/pull/3") {
		t.Fatalf("watch output = %s", out2.String())
	}
	if time.Since(start) < time.Second {
		t.Fatal("watch must wait the interval between polls")
	}
}

func TestPlanStatus_WatchEndsOnCancelAndRejectsShortIntervals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: func(recordedRequest) fakeReply {
			if atomic.AddInt32(&polls, 1) == 2 {
				cancel()
			}
			return fakeReply{Body: planDetail("open")}
		},
	})
	f, out := testFactory(srv)
	cmd := NewPlanCmd(f)
	cmd.SetArgs([]string{"status", plPlan, "--watch", "--json", "--interval", "1s"})
	if err := cmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("cancelled watch must end cleanly: %v", err)
	}
	if !strings.Contains(out.String(), `"status": "open"`) {
		t.Fatalf("output = %s", out.String())
	}
	f2, _ := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "status", plPlan, "--interval", "10ms"); err == nil || !strings.Contains(err.Error(), "at least 1s") {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanStatus_WatchReturnsDeadlineErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: reply(200, planDetail("open")),
	})
	f, _ := testFactory(srv)
	cmd := NewPlanCmd(f)
	cmd.SetArgs([]string{"status", plPlan, "--watch", "--interval", "1s"})
	err := cmd.ExecuteContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("error = %v, want the deadline", err)
	}
}

func TestPlanList(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": reply(200, map[string]any{"data": []any{map[string]any{"id": plPlan, "status": "open", "name": "Checkout", "repository": plRepo}}, "total": 1}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "list", "--product", plProduct, "--status", "open", "--page", "2", "--per-page", "10"); err != nil {
		t.Fatalf("list: %v", err)
	}
	q := api.calls()[0].Query
	if q.Get("status") != "open" || q.Get("page") != "2" || q.Get("per_page") != "10" {
		t.Fatalf("query = %v", q)
	}
	for _, want := range []string{plPlan, "open", "Checkout", "1 of 1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table misses %q:\n%s", want, out.String())
		}
	}
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "list", "--product", plProduct, "--json"); err != nil {
		t.Fatalf("list json: %v", err)
	}
	if !strings.Contains(out2.String(), `"total": 1`) {
		t.Fatalf("json = %s", out2.String())
	}
	f3, _ := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "list"); err == nil {
		t.Fatal("list without a product must fail")
	}
}

func TestPlanList_RefusesAnUnknownStatusLocally(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{})
	f, _ := testFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "list", "--product", plProduct, "--status", "running")
	if err == nil || err.Error() != `--status must be one of draft, decomposing, graph_proposed, open, integrating, held, verifying, final_pr_open, merged or abandoned, got "running"` {
		t.Fatalf("error = %v", err)
	}
	if len(api.calls()) != 0 {
		t.Fatalf("an unknown status sent requests: %v", api.summary())
	}
	cmd, _, err := NewPlanCmd(f).Find([]string{"list"})
	if err != nil {
		t.Fatal(err)
	}
	if usage := cmd.Flags().Lookup("status").Usage; usage != "filter by status: draft, decomposing, graph_proposed, open, integrating, held, verifying, final_pr_open, merged or abandoned" {
		t.Fatalf("--status help = %q", usage)
	}
}

func TestPlanList_RefusesAProductThatIsNotAUUIDLocally(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{})
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"list", "--product", "not-a-uuid"}, `--product must be a UUID, got "not-a-uuid"`},
		{[]string{"list", "--product", "not-a-uuid", "--status", "open"}, `--product must be a UUID, got "not-a-uuid"`},
		{[]string{"list"}, "--product is required"},
	} {
		f, out := testFactory(srv)
		if err := runCmd(t, NewPlanCmd(f), tc.args...); err == nil || err.Error() != tc.want || out.Len() != 0 {
			t.Errorf("%v: error = %v, output %q, want %q", tc.args, err, out.String(), tc.want)
		}
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("an invalid product sent %d requests: %v", n, api.summary())
	}
}

func TestPlanApprove_HelpNamesDraftPlans(t *testing.T) {
	cmd, _, err := NewPlanCmd(&Factory{Out: &bytes.Buffer{}}).Find([]string{"approve"})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Short != "Approve a draft work plan and queue its draft orders" {
		t.Fatalf("help = %q", cmd.Short)
	}
}

func columnsAligned(t *testing.T, out, headerStart, rowStart string, pairs [][2]string) {
	t.Helper()
	var header, row string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, headerStart):
			header = line
		case strings.HasPrefix(line, rowStart):
			row = line
		}
	}
	if header == "" || row == "" {
		t.Fatalf("table header %q or row %q missing:\n%s", headerStart, rowStart, out)
	}
	for _, p := range pairs {
		if h, v := strings.Index(header, p[0]), strings.Index(row, p[1]); h < 0 || h != v {
			t.Errorf("column %s starts at %d but its value %q at %d:\n%s\n%s", p[0], h, p[1], v, header, row)
		}
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("a control sequence reached the terminal: %q", out)
	}
}

func TestPlanTablesKeepColumnsAligned(t *testing.T) {
	fu := "dddddddd-0000-4000-8000-000000000001"
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": reply(200, map[string]any{"data": []any{map[string]any{"id": plPlan, "status": "open", "name": "Check\tout\x1b[2J", "repository": plRepo}}, "total": 1}),
		"GET /api/work-plans/" + plPlan + "/followups":   reply(200, map[string]any{"data": []any{map[string]any{"id": fu, "title": "Retry\twebhooks", "severity": "high", "state": "open"}}, "total": 1}),
		"GET /api/work-plans/" + plPlan:                  reply(200, planDetail("open", map[string]any{"id": "o1", "status": "blocked", "blocked_kind": "worker\terror", "attempt": 2, "pr_url": "https://github.com/acme/app/pull/3"})),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "list", "--product", plProduct); err != nil {
		t.Fatalf("list: %v", err)
	}
	columnsAligned(t, out.String(), "ID", plPlan, [][2]string{{"STATUS", "open"}, {"NAME", "Check out[2J"}, {"REPOSITORY", plRepo}})
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "followups", plPlan); err != nil {
		t.Fatalf("followups: %v", err)
	}
	columnsAligned(t, out2.String(), "ID", fu, [][2]string{{"STATE", "open"}, {"SEVERITY", "high"}, {"TITLE", "Retry webhooks"}})
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "status", plPlan); err != nil {
		t.Fatalf("status: %v", err)
	}
	columnsAligned(t, out3.String(), "ORDER", "o1", [][2]string{{"STATUS", "blocked(worker error)"}, {"ATTEMPT", "2"}, {"PR", "https://github.com/acme/app/pull/3"}})
}

func TestPlanDecisions(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-plans/" + plPlan + "/approve":       reply(200, planDetail("open", map[string]any{"id": "o1", "status": "queued"})),
		"POST /api/work-plans/" + plPlan + "/abandon":       reply(200, planDetail("abandoned")),
		"POST /api/work-plans/" + plPlan + "/confirm-merge": reply(200, planDetail("merged")),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "approve", plPlan); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if api.only(t, "POST", "/api/work-plans/"+plPlan+"/approve").Raw != "" {
		t.Fatal("approve sends no body")
	}
	if !strings.Contains(out.String(), `"Checkout": open`) || !strings.Contains(out.String(), "queued") {
		t.Fatalf("approve output = %s", out.String())
	}
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "abandon", plPlan, "--confirm-pr-closed"); err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if b := api.only(t, "POST", "/api/work-plans/"+plPlan+"/abandon").Body; b["confirmPrClosed"] != true {
		t.Fatalf("abandon body = %v", b)
	}
	if !strings.Contains(out2.String(), "abandoned") {
		t.Fatalf("abandon output = %s", out2.String())
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "confirm-merge", plPlan, "--json"); err != nil {
		t.Fatalf("confirm-merge: %v", err)
	}
	if !strings.Contains(out3.String(), `"status": "merged"`) {
		t.Fatalf("confirm-merge output = %s", out3.String())
	}
}

func TestPlanDecisions_InteractiveLoginRefusalAndInvalidResponse(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-plans/" + plPlan + "/approve": reply(403, `{"code":"interactive_user_required","message":"this decision needs a signed-in person; API keys cannot make it"}`),
		"POST /api/work-plans/" + plPlan + "/abandon": reply(200, `[]`),
	})
	f, _ := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "approve", plPlan); err == nil || !strings.Contains(err.Error(), "API keys cannot make it (status 403)") {
		t.Fatalf("error = %v", err)
	}
	f2, _ := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "abandon", plPlan); err == nil || !strings.Contains(err.Error(), "invalid work plan response") {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanFinalize(t *testing.T) {
	calls := 0
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-plans/" + plPlan + "/finalize": func(r recordedRequest) fakeReply {
			calls++
			if calls == 1 {
				return fakeReply{Body: map[string]any{"plan": planDetail("integrating"), "compare_url": "https://github.com/acme/app/compare/main...plan/checkout", "recorded_pr": false, "open_orders": 0, "merged_orders": 2}}
			}
			p := planDetail("final_pr_open")
			p["final_pr_url"] = "https://github.com/acme/app/pull/12"
			return fakeReply{Body: map[string]any{"plan": p, "recorded_pr": true, "open_orders": 0, "merged_orders": 2}}
		},
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "finalize", plPlan); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if b := api.calls()[0]; b.Raw != "{}" {
		t.Fatalf("finalize without a PR sends an empty object, got %q", b.Raw)
	}
	if out.String() != "Plan "+plPlan+": integrating; 2 merged, 0 open order(s); open the final PR at https://github.com/acme/app/compare/main...plan/checkout\n" {
		t.Fatalf("output = %q", out.String())
	}
	f2, out2 := testFactory(srv)
	head := strings.Repeat("d", 40)
	if err := runCmd(t, NewPlanCmd(f2), "finalize", plPlan, "--pr-url", "https://github.com/acme/app/pull/12", "--pr-number", "12", "--head-sha", head); err != nil {
		t.Fatalf("finalize with PR: %v", err)
	}
	b := api.calls()[1].Body
	if b["prUrl"] != "https://github.com/acme/app/pull/12" || b["prNumber"] != float64(12) || b["headSha"] != head {
		t.Fatalf("body = %v", b)
	}
	if out2.String() != "Plan "+plPlan+": final_pr_open; 2 merged, 0 open order(s); recorded final PR https://github.com/acme/app/pull/12\n" {
		t.Fatalf("output = %q", out2.String())
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "finalize", plPlan, "--json"); err != nil {
		t.Fatalf("finalize json: %v", err)
	}
	if !strings.Contains(out3.String(), `"recorded_pr": true`) {
		t.Fatalf("json = %s", out3.String())
	}
}

func TestPlanFinalize_InvalidResponse(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-plans/" + plPlan + "/finalize": reply(200, `[]`),
	})
	f, _ := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "finalize", plPlan); err == nil || !strings.Contains(err.Error(), "invalid finalize response") {
		t.Fatalf("error = %v", err)
	}
}

func followupJSON(id, state string) map[string]any {
	return map[string]any{"id": id, "work_order_id": "eeeeeeee-0000-4000-8000-000000000001", "title": "Retry webhooks", "rationale": "Flaky provider", "paths": []any{"backend/webhooks.go"}, "severity": "high", "state": state, "reported_at": "2026-09-28T10:00:00Z", "decided_at": nil, "decided_by_user_id": nil}
}

func TestPlanFollowups(t *testing.T) {
	fu := "dddddddd-0000-4000-8000-000000000001"
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan + "/followups":                     reply(200, map[string]any{"data": []any{followupJSON(fu, "open")}, "total": 1, "page": 1, "perPage": 20}),
		"POST /api/work-plans/" + plPlan + "/followups/" + fu + "/accept":  reply(200, followupJSON(fu, "accepted")),
		"POST /api/work-plans/" + plPlan + "/followups/" + fu + "/dismiss": reply(200, followupJSON(fu, "dismissed")),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "followups", plPlan, "--page", "1", "--per-page", "20"); err != nil {
		t.Fatalf("followups: %v", err)
	}
	if q := api.calls()[0].Query; q.Get("page") != "1" || q.Get("per_page") != "20" {
		t.Fatalf("query = %v", q)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "STATE") || strings.Contains(lines[0], "STATUS") || lines[2] != "1 of 1" {
		t.Fatalf("table:\n%s", out.String())
	}
	columnsAligned(t, out.String(), "ID", fu, [][2]string{{"STATE", "open"}, {"SEVERITY", "high"}, {"TITLE", "Retry webhooks"}})
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "followup", "accept", plPlan, fu); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if out2.String() != "Follow-up "+fu+" is accepted\n" {
		t.Fatalf("accept output = %q", out2.String())
	}
	if raw := api.only(t, "POST", "/api/work-plans/"+plPlan+"/followups/"+fu+"/accept").Raw; raw != "" {
		t.Fatalf("accept sends no body, got %q", raw)
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "followup", "dismiss", plPlan, fu); err != nil {
		t.Fatalf("dismiss: %v", err)
	}
	if out3.String() != "Follow-up "+fu+" is dismissed\n" {
		t.Fatalf("dismiss output = %q", out3.String())
	}
	f4, out4 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f4), "followup", "dismiss", plPlan, fu, "--json"); err != nil {
		t.Fatalf("dismiss json: %v", err)
	}
	if !strings.Contains(out4.String(), `"state": "dismissed"`) {
		t.Fatalf("dismiss output = %q", out4.String())
	}
	f5, out5 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f5), "followups", plPlan, "--json"); err != nil {
		t.Fatalf("followups json: %v", err)
	}
	if !strings.Contains(out5.String(), `"total": 1`) {
		t.Fatalf("json = %s", out5.String())
	}
}

func TestPlanFollowups_InvalidResponses(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan + "/followups":           reply(200, `[]`),
		"POST /api/work-plans/" + plPlan + "/followups/x/accept": reply(200, `[]`),
		"GET /api/work-plans/" + plPlan:                          reply(200, `[]`),
		"GET /api/products/" + plProduct + "/work-plans":         reply(200, `[]`),
	})
	for _, args := range [][]string{{"followups", plPlan}, {"followup", "accept", plPlan, "x"}, {"status", plPlan}, {"list", "--product", plProduct}} {
		f, _ := testFactory(srv)
		if err := runCmd(t, NewPlanCmd(f), args...); err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("%v: error = %v", args, err)
		}
	}
}

func TestPlanPush_RefusesAPIKeysAndSessionTokensBeforeAnyWrite(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, _ := testFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	if err == nil || !strings.Contains(err.Error(), "needs the browser login of `zensu auth login`; API keys and session tokens cannot create work plans, so nothing was created") {
		t.Fatalf("error = %v", err)
	}
	f2, _ := sessionFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, validPlanFile)); err == nil || !strings.Contains(err.Error(), "cannot create work plans") {
		t.Fatalf("session token error = %v", err)
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("a refused push sent requests: %v", api.summary())
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "push", writePlanFile(t, validPlanFile), "--dry-run"); err != nil {
		t.Fatalf("a dry run needs no login: %v", err)
	}
	if !strings.Contains(out3.String(), "would create feature") {
		t.Fatalf("dry run output = %s", out3.String())
	}
	assertNoWrites(t, api)
}

func TestPlanCommandsFailOnTransportErrors(t *testing.T) {
	f, _ := failingFactory()
	for _, args := range [][]string{
		{"status", plPlan}, {"list", "--product", plProduct}, {"approve", plPlan}, {"finalize", plPlan},
		{"followups", plPlan}, {"followup", "accept", plPlan, "x"},
		{"push", writePlanFile(t, validPlanFile), "--dry-run"},
	} {
		if err := runCmd(t, NewPlanCmd(f), args...); err == nil || !strings.Contains(err.Error(), "dial refused") {
			t.Errorf("%v: error = %v", args, err)
		}
	}
}
