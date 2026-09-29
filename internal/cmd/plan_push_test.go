package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

type pushEnvelope struct {
	DryRun        bool           `json:"dry_run"`
	AlreadyPushed bool           `json:"already_pushed"`
	Notes         []string       `json:"notes"`
	Request       map[string]any `json:"request"`
	Plan          map[string]any `json:"plan"`
}

func decodePushResult(t *testing.T, out string) (pushEnvelope, []string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(out))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%s", err, out)
	}
	var rest json.RawMessage
	if err := dec.Decode(&rest); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout holds more than one JSON document (%v):\n%s", err, out)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, out)
	}
	var env pushEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope: %v", err)
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	return env, names
}

func routeSet(key string, handler func(recordedRequest) fakeReply) map[string]func(recordedRequest) fakeReply {
	return map[string]func(recordedRequest) fakeReply{key: handler}
}

func TestPlanPush_RefusesBeforeAnyWrite(t *testing.T) {
	const otherRevision = "bbbbbbbb-0000-4000-8000-0000000000ff"
	subfeature := featureRow(plFeatureR, "refund-export", "in_development", plRevision)
	subfeature["parent_feature_id"] = plFeature42
	shipped := featureRow(plFeatureR, "refund-export", "shipped", plRevision)
	otherComponent := featureRow(plFeatureR, "refund-export", "planned", plRevision)
	otherComponent["component_id"] = "77777777-8888-4999-8aaa-cccccccccccc"
	subZen := featureRow(plFeature42, "zen-42", "in_development", plRev42)
	subZen["parent_feature_id"] = plFeature7
	listed := func(rows ...map[string]any) map[string]func(recordedRequest) fakeReply {
		return routeSet("GET /api/features", reply(200, featureList(rows...)))
	}
	resolved := func(row map[string]any) map[string]func(recordedRequest) fakeReply {
		return routeSet("GET /api/features/resolve", reply(200, row))
	}
	calls := func(extra ...string) []string {
		return append([]string{"GET /api/products/" + plProduct + "/work-plans", "GET /api/products/" + plProduct + "/repositories"}, extra...)
	}
	title := plHead + "  - title: Refund export\n    component: " + plComponent + "\n"
	label := `"Feature refund-export" (` + plFeatureR + `)`
	cases := []struct {
		name  string
		file  string
		extra map[string]func(recordedRequest) fakeReply
		want  string
		calls []string
	}{
		{
			name:  "unregistered repository",
			file:  plHead + "  - feature: ZEN-42\n---\n",
			extra: routeSet("GET /api/products/"+plProduct+"/repositories", reply(200, map[string]any{"data": []any{map[string]any{"repository": "github.com/acme/other"}}})),
			want:  "front matter: repository github.com/acme/app is not registered for product " + plProduct + "; register it with `zensu work repositories add` before pushing the plan",
			calls: calls(),
		},
		{
			name:  "unknown task",
			file:  strings.Replace(plHead, "name: P\n", "name: P\ntask: "+plTask+"\n", 1) + "  - feature: ZEN-42\n---\n",
			extra: routeSet("GET /api/tasks/"+plTask, reply(404, `{"code":"not_found","message":"task not found"}`)),
			want:  "front matter: task " + plTask + ": task not found (status 404)",
			calls: calls("GET /api/tasks/" + plTask),
		},
		{
			name:  "revision that is not active in the product",
			file:  plHead + "  - revision: " + otherRevision + "\n---\n",
			want:  "item 1: revision " + otherRevision + " is not the active revision of a feature of this product",
			calls: calls("GET /api/features"),
		},
		{
			name:  "revision of a shipped feature",
			file:  plHead + "  - revision: " + plRevision + "\n---\n",
			extra: listed(shipped),
			want:  "item 1: revision " + plRevision + " of feature " + label + " is shipped; plan the feature with new_revision and a scope_summary",
			calls: calls("GET /api/features"),
		},
		{
			name:  "revision of a subfeature",
			file:  plHead + "  - revision: " + plRevision + "\n---\n",
			extra: listed(subfeature),
			want:  "item 1: revision " + plRevision + " belongs to subfeature " + label + "; plan its parent feature",
			calls: calls("GET /api/features"),
		},
		{
			name:  "feature list fails for a revision item",
			file:  plHead + "  - revision: " + plRevision + "\n---\n",
			extra: routeSet("GET /api/features", reply(200, `{"data":[1],"total":1,"perPage":100}`)),
			want:  "item 1: invalid feature list: json: cannot unmarshal number into Go value of type cmd.pushedFeature",
			calls: calls("GET /api/features"),
		},
		{
			name:  "subfeature",
			file:  plHead + "  - feature: ZEN-42\n---\n",
			extra: resolved(subZen),
			want:  "item 1: ZEN-42 is a subfeature; plan its parent feature",
			calls: calls("GET /api/features/resolve"),
		},
		{
			name:  "feature without an active revision",
			file:  plHead + "  - feature: ZEN-42\n---\n",
			extra: resolved(featureRow(plFeature42, "zen-42", "", "")),
			want:  "item 1: the active revision of ZEN-42 is missing; set new_revision with a scope_summary to plan a new revision",
			calls: calls("GET /api/features/resolve"),
		},
		{
			name:  "slug of a subfeature",
			file:  title + "---\n",
			extra: listed(subfeature),
			want:  "item 1: slug refund-export belongs to subfeature " + label + "; set slug to name a new feature",
			calls: calls("GET /api/features"),
		},
		{
			name:  "slug in another component",
			file:  title + "---\n",
			extra: listed(otherComponent),
			want:  "item 1: slug refund-export belongs to feature " + label + " in another component; set slug to name a new feature, or plan that feature by its ID",
			calls: calls("GET /api/features"),
		},
		{
			name:  "slug of a shipped feature",
			file:  title + "---\n",
			extra: listed(shipped),
			want:  "item 1: slug refund-export belongs to feature " + label + ", whose active revision is shipped; plan that feature with new_revision and a scope_summary, or set slug to name a new feature",
			calls: calls("GET /api/features"),
		},
		{
			name:  "component outside the product",
			file:  plHead + "  - title: Refund export\n    component: 77777777-8888-4999-8aaa-dddddddddddd\n---\n",
			extra: listed(),
			want:  "item 1: component 77777777-8888-4999-8aaa-dddddddddddd is not a component of product " + plProduct,
			calls: calls("GET /api/features", "GET /api/products/"+plProduct+"/components"),
		},
		{
			name:  "description over the limit",
			file:  title + "    scope_summary: " + strings.Repeat("ä", 5000) + strings.Repeat("a", 5001) + "\n---\n",
			extra: listed(),
			want:  "item 1: the feature description built from scope_summary and the requirement texts has 10001 characters; Zensu accepts at most 10000",
			calls: calls("GET /api/features", "GET /api/products/"+plProduct+"/components"),
		},
		{
			name:  "revision item repeating the active revision of a feature item",
			file:  plHead + "  - feature: ZEN-42\n  - revision: " + plRev42 + "\n---\n",
			extra: listed(featureRow(plFeature42, "zen-42", "in_development", plRev42)),
			want:  "items 1 and 2 plan the same feature " + plFeature42,
			calls: calls("GET /api/features/resolve", "GET /api/features"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, planPushRoutes(tc.extra))
			f, out := loginFactory(srv)
			err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, tc.file))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v\nwant    %s", err, tc.want)
			}
			assertNoWrites(t, api)
			assertCalls(t, api, tc.calls...)
			if out.Len() != 0 {
				t.Fatalf("a refused push printed %q", out.String())
			}
		})
	}
}

func TestPlanPush_SendsADescriptionOfTheLimitAndOmitsAnEmptyOne(t *testing.T) {
	scope := strings.Repeat("ä", 5000) + strings.Repeat("a", 5000)
	file := plHead + "  - title: Refund export\n    component: " + plComponent + "\n    scope_summary: " + scope + "\n  - title: Refund audit\n    component: " + plComponent + "\n---\n"
	var bodies []map[string]any
	var mu sync.Mutex
	srv, _ := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/features": reply(200, featureList()),
		"POST /api/features": func(r recordedRequest) fakeReply {
			mu.Lock()
			bodies = append(bodies, r.Body)
			n := len(bodies)
			mu.Unlock()
			return fakeReply{Status: 201, Body: map[string]any{"id": "aaaaaaaa-0000-4000-8000-00000000010" + strconv.Itoa(n)}}
		},
	}))
	f, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file)); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("feature bodies = %v", bodies)
	}
	if d, _ := bodies[0]["description"].(string); utf8.RuneCountInString(d) != 10000 || d != scope {
		t.Fatalf("description has %d characters", utf8.RuneCountInString(d))
	}
	if _, ok := bodies[1]["description"]; ok || bodies[1]["slug"] != "refund-audit" {
		t.Fatalf("a feature without scope or requirement texts must not send a description: %v", bodies[1])
	}
}

type pushLog struct {
	mu     sync.Mutex
	events []string
}

func (l *pushLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		l.events = append(l.events, "out: "+line)
	}
	return len(p), nil
}

func (l *pushLog) record(routes map[string]func(recordedRequest) fakeReply) map[string]func(recordedRequest) fakeReply {
	for key, handler := range routes {
		key, handler := key, handler
		routes[key] = func(r recordedRequest) fakeReply {
			l.mu.Lock()
			l.events = append(l.events, key)
			l.mu.Unlock()
			return handler(r)
		}
	}
	return routes
}

func TestPlanPush_PrintsLookupNotesBeforeTheFirstWrite(t *testing.T) {
	reuse := routeSet("GET /api/features", reply(200, featureList(featureRow(plNewFeat, "refund-webhook", "planned", "bbbbbbbb-0000-4000-8000-00000000000f"))))
	log := &pushLog{}
	srv, _ := newFakeAPI(t, log.record(planPushRoutes(reuse)))
	f, _ := loginFactory(srv)
	f.Out = log
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile)); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	want := []string{
		"GET /api/products/" + plProduct + "/work-plans",
		"GET /api/products/" + plProduct + "/repositories",
		"GET /api/features/resolve",
		"GET /api/features/resolve",
		"GET /api/features",
		"out: item 1: ZEN-42 already exists, so the texts of AC-001, AC-002 are ignored; the plan records only requirement IDs and tags",
		"out: item 3: reusing feature refund-webhook (" + plNewFeat + ")",
		"out: item 3: feature refund-webhook already exists, so the texts of IF-001 are ignored; the plan records only requirement IDs and tags",
		"POST /api/features/" + plFeature7 + "/revisions",
		"out: item 2: created revision v2 of ZEN-7",
		"POST /api/products/" + plProduct + "/work-plans",
		"out: Pushed plan " + plPlan + " with 3 work order(s).",
	}
	if len(log.events) < len(want) || strings.Join(log.events[:len(want)], "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s\nwant them to start with:\n%s", strings.Join(log.events, "\n"), strings.Join(want, "\n"))
	}
	srv2, _ := newFakeAPI(t, planPushRoutes(reuse))
	f2, out2 := loginFactory(srv2)
	if err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, validPlanFile), "--json"); err != nil {
		t.Fatalf("plan push --json: %v", err)
	}
	env, _ := decodePushResult(t, out2.String())
	wantNotes := []string{
		"item 1: ZEN-42 already exists, so the texts of AC-001, AC-002 are ignored; the plan records only requirement IDs and tags",
		"item 3: reusing feature refund-webhook (" + plNewFeat + ")",
		"item 3: feature refund-webhook already exists, so the texts of IF-001 are ignored; the plan records only requirement IDs and tags",
		"item 2: created revision v2 of ZEN-7",
	}
	if strings.Join(env.Notes, "\n") != strings.Join(wantNotes, "\n") {
		t.Fatalf("notes:\n%s", strings.Join(env.Notes, "\n"))
	}
}

type brokenAfter struct {
	left int
	err  error
}

func (w *brokenAfter) Write(p []byte) (int, error) {
	if w.left == 0 {
		return 0, w.err
	}
	w.left--
	return len(p), nil
}

func TestPlanPush_ReturnsOutputErrors(t *testing.T) {
	broken := errors.New("stdout closed")
	revisionOnly := plHead + "  - revision: " + plRevision + "\n---\n"
	cases := []struct {
		name   string
		file   string
		writes int
		posts  int
	}{
		{name: "lookup notes", file: validPlanFile, writes: 0, posts: 0},
		{name: "notes of a write", file: validPlanFile, writes: 1, posts: 1},
		{name: "result line", file: revisionOnly, writes: 0, posts: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, planPushRoutes(nil))
			f, _ := loginFactory(srv)
			f.Out = &brokenAfter{left: tc.writes, err: broken}
			err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, tc.file))
			if !errors.Is(err, broken) {
				t.Fatalf("error = %v", err)
			}
			posts := 0
			for _, c := range api.summary() {
				if strings.HasPrefix(c, "POST ") {
					posts++
				}
			}
			if posts != tc.posts {
				t.Fatalf("writes before the output failed: %v", api.summary())
			}
		})
	}
}

func TestPlanPush_NeedsAClient(t *testing.T) {
	f := &Factory{Out: &bytes.Buffer{}, NewClient: func(context.Context) (*client.Client, error) {
		return nil, errors.New("no login stored")
	}}
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	if err == nil || err.Error() != "no login stored" {
		t.Fatalf("error = %v", err)
	}
}

func TestPlanPush_JSONEnvelopeInEveryMode(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile), "--dry-run", "--json"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	assertNoWrites(t, api)
	env, keys := decodePushResult(t, out.String())
	if strings.Join(keys, ",") != "already_pushed,dry_run,notes,request" || !env.DryRun || env.AlreadyPushed {
		t.Fatalf("dry run envelope = %+v (keys %v)", env, keys)
	}
	wantNotes := []string{
		"item 1: ZEN-42 already exists, so the texts of AC-001, AC-002 are ignored; the plan records only requirement IDs and tags",
		"item 2: would create a new revision of ZEN-7",
		`item 3: would create feature "Refund webhook" (refund-webhook)`,
	}
	if strings.Join(env.Notes, "\n") != strings.Join(wantNotes, "\n") {
		t.Fatalf("dry run notes:\n%s", strings.Join(env.Notes, "\n"))
	}
	wantRequest := map[string]any{"name": "Checkout hardening", "repository": "https://github.com/acme/app", "sourceKind": "plan_file", "sourcePath": "plan.md", "contentHash": hashOf(validPlanFile), "createdVia": "cli", "baseBranch": "develop", "integrationBranch": "plan/checkout", "preferredHarness": "claude"}
	for k, v := range wantRequest {
		if env.Request[k] != v {
			t.Errorf("request[%s] = %v, want %v", k, env.Request[k], v)
		}
	}
	items := env.Request["items"].([]any)
	if len(env.Request) != len(wantRequest)+1 || len(items) != 3 || items[0].(map[string]any)["featureId"] != plFeature42 || items[1].(map[string]any)["featureId"] != plFeature7 || items[2].(map[string]any)["featureId"] != "" {
		t.Fatalf("request = %v", env.Request)
	}

	f2, out2 := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, validPlanFile), "--json"); err != nil {
		t.Fatalf("push: %v", err)
	}
	env2, keys2 := decodePushResult(t, out2.String())
	if strings.Join(keys2, ",") != "already_pushed,dry_run,notes,plan" || env2.DryRun || env2.AlreadyPushed {
		t.Fatalf("push envelope = %+v (keys %v)", env2, keys2)
	}
	wantNotes2 := []string{wantNotes[0], "item 2: created revision v2 of ZEN-7", `item 3: created feature "Refund webhook" (` + plNewFeat + `)`}
	if strings.Join(env2.Notes, "\n") != strings.Join(wantNotes2, "\n") {
		t.Fatalf("push notes:\n%s", strings.Join(env2.Notes, "\n"))
	}
	if env2.Plan["id"] != plPlan || env2.Plan["status"] != "draft" || env2.Plan["created"] != true || len(env2.Plan["orders"].([]any)) != 3 {
		t.Fatalf("plan = %v", env2.Plan)
	}

	listed := map[string]any{"data": []any{map[string]any{"id": plPlan, "status": "open", "source_ref": map[string]any{"content_hash": hashOf(validPlanFile)}}}, "total": 1, "perPage": 100}
	srv3, api3 := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": reply(200, listed),
		"GET /api/work-plans/" + plPlan:                  reply(200, planDetail("open")),
	}))
	f3, out3 := testFactory(srv3)
	if err := runCmd(t, NewPlanCmd(f3), "push", writePlanFile(t, validPlanFile), "--dry-run", "--json"); err != nil {
		t.Fatalf("dry run of a pushed file: %v", err)
	}
	assertCalls(t, api3, "GET /api/products/"+plProduct+"/work-plans", "GET /api/work-plans/"+plPlan)
	env3, keys3 := decodePushResult(t, out3.String())
	if strings.Join(keys3, ",") != "already_pushed,dry_run,notes,plan" || !env3.DryRun || !env3.AlreadyPushed || len(env3.Notes) != 0 || env3.Plan["id"] != plPlan {
		t.Fatalf("pushed dry run envelope = %+v (keys %v)", env3, keys3)
	}
}

func TestPlanPush_AlreadyPushedJSONNeedsThePlan(t *testing.T) {
	listed := map[string]any{"data": []any{map[string]any{"id": plPlan, "status": "open", "source_ref": map[string]any{"content_hash": hashOf(validPlanFile)}}}, "total": 1, "perPage": 100}
	cases := map[string]fakeReply{
		"no plan (status 404)":         {Status: 404, Body: `{"code":"not_found","message":"no plan"}`},
		"invalid work plan response: ": {Status: 200, Body: `[]`},
	}
	for want, detail := range cases {
		t.Run(want, func(t *testing.T) {
			srv, _ := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
				"GET /api/products/" + plProduct + "/work-plans": reply(200, listed),
				"GET /api/work-plans/" + plPlan:                  reply(detail.Status, detail.Body),
			}))
			f, out := loginFactory(srv)
			err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile), "--json")
			if err == nil || !strings.HasPrefix(err.Error(), want) {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if out.Len() != 0 {
				t.Fatalf("output = %q", out.String())
			}
		})
	}
}

func TestPlanPush_KeepsServerControlSequencesOffTheTerminal(t *testing.T) {
	srv, _ := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"POST /api/features/" + plFeature7 + "/revisions": reply(201, map[string]any{"id": plRevision, "version": "v2\x1b[31m"}),
		"POST /api/features":                              reply(409, `{"code":"conflict","message":"feature with this slug already exists"}`),
	}))
	f, out := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	want := `item 3: create feature "Refund webhook": feature with this slug already exists (status 409); created before the failure: revision v2[31m of ZEN-7; pushing the plan again reuses them`
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(out.String(), "item 2: created revision v2[31m of ZEN-7\n") || strings.ContainsRune(out.String(), 0x1b) {
		t.Fatalf("output = %q", out.String())
	}

	evil := featureRow(plFeatureR, "refund-export", "planned", plRevision)
	evil["title"] = "Evil\x1b]0;owned\x07"
	evil["component_id"] = "77777777-8888-4999-8aaa-cccccccccccc"
	srv2, _ := newFakeAPI(t, planPushRoutes(routeSet("GET /api/features", reply(200, featureList(evil)))))
	f2, _ := loginFactory(srv2)
	err = runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, plHead+"  - title: Refund export\n    component: "+plComponent+"\n---\n"))
	if err == nil || err.Error() != `item 1: slug refund-export belongs to feature "Evil]0;owned" (`+plFeatureR+`) in another component; set slug to name a new feature, or plan that feature by its ID` {
		t.Fatalf("error = %q", err)
	}

	dirtyID := plFeature42 + "\x1b[2J"
	row := featureRow(dirtyID, "zen-42", "in_development\x1b[0m", plRev42)
	srv3, _ := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/features/resolve":        reply(200, row),
		"GET /api/features/" + plFeature42: reply(200, row),
	}))
	f3, _ := loginFactory(srv3)
	err = runCmd(t, NewPlanCmd(f3), "push", writePlanFile(t, plHead+"  - feature: ZEN-42\n  - feature: "+plFeature42+"\n---\n"))
	if err == nil || err.Error() != "items 1 and 2 plan the same feature "+plFeature42+"[2J" {
		t.Fatalf("error = %q", err)
	}
	f4, out4 := loginFactory(srv3)
	file := plHead + "  - feature: ZEN-42\n    new_revision: true\n    scope_summary: S\n---\n"
	if err := runCmd(t, NewPlanCmd(f4), "push", writePlanFile(t, file), "--dry-run", "--json"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	env, _ := decodePushResult(t, out4.String())
	if len(env.Notes) != 1 || env.Notes[0] != "item 1: ZEN-42 already has an open revision (in_development[0m); the plan targets it" {
		t.Fatalf("notes = %q", env.Notes)
	}
}

func TestPlanPush_StopsAtTheListPageCap(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": func(r recordedRequest) fakeReply {
			return fakeReply{Body: map[string]any{"data": []any{map[string]any{"id": "p" + r.Query.Get("page"), "status": "open", "source_ref": map[string]any{"content_hash": "other"}}}, "total": 1000, "perPage": 1}}
		},
	}))
	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	if err == nil || err.Error() != "/api/products/"+plProduct+"/work-plans has more than 50 pages; refusing to decide on a partial list" {
		t.Fatalf("error = %v", err)
	}
	if n := api.count("GET", "/api/products/"+plProduct+"/work-plans"); n != 50 {
		t.Fatalf("pages read = %d, want 50", n)
	}
	assertNoWrites(t, api)
}

func TestEachPage_ReadsUpToTheLastPageOfTheCap(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/list": func(r recordedRequest) fakeReply {
			return fakeReply{Body: map[string]any{"data": []any{r.Query.Get("page")}, "total": 50}}
		},
	})
	f, _ := testFactory(srv)
	var seen []string
	err := eachPage(context.Background(), f, "/api/list", func(item json.RawMessage) (bool, error) {
		seen = append(seen, string(item))
		return false, nil
	})
	if err != nil || len(seen) != 50 || seen[49] != `"50"` {
		t.Fatalf("eachPage = %v after %d items", err, len(seen))
	}
	if q := api.calls()[49].Query; q.Get("page") != "50" || q.Get("per_page") != "100" {
		t.Fatalf("last query = %v", q)
	}
}

func TestPlanPush_RejectsAMalformedPlanList(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(routeSet("GET /api/products/"+plProduct+"/work-plans", reply(200, `{"data":[1],"total":1,"perPage":100}`))))
	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, validPlanFile))
	if err == nil || err.Error() != "invalid work plan list: json: cannot unmarshal number into Go value of type cmd.workPlanView" {
		t.Fatalf("error = %v", err)
	}
	assertNoWrites(t, api)
}

func TestPlanSourcePath_RecordsThePathInsideTheGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	gitInit := exec.Command("git", "init", "-q")
	gitInit.Dir = repo
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	plans := filepath.Join(repo, "docs", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(plans, "refunds.md")
	if err := os.WriteFile(file, []byte(validPlanFile), 0o600); err != nil {
		t.Fatal(err)
	}
	const want = "docs/plans/refunds.md"
	if got := planSourcePath(file); got != want {
		t.Fatalf("absolute path: %q, want %q", got, want)
	}
	for _, tc := range []struct{ wd, arg string }{
		{repo, filepath.Join("docs", "plans", "refunds.md")},
		{filepath.Join(repo, "docs"), filepath.Join("plans", "refunds.md")},
		{plans, "refunds.md"},
		{filepath.Join(repo, "docs", "plans"), filepath.Join("..", "plans", "refunds.md")},
	} {
		t.Chdir(tc.wd)
		if got := planSourcePath(tc.arg); got != want {
			t.Fatalf("from %s with %s: %q, want %q", tc.wd, tc.arg, got, want)
		}
	}
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, _ := loginFactory(srv)
	t.Chdir(filepath.Join(repo, "docs"))
	if err := runCmd(t, NewPlanCmd(f), "push", filepath.Join("plans", "refunds.md")); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	if got := api.only(t, "POST", "/api/products/"+plProduct+"/work-plans").Body["sourcePath"]; got != want {
		t.Fatalf("sourcePath = %v, want %s", got, want)
	}
}

func TestPlanPush_NotesRequirementTextsOfExistingFeatures(t *testing.T) {
	const featureX = "aaaaaaaa-0000-4000-8000-0000000000a2"
	file := plHead +
		"  - feature: ZEN-42\n    requirements: [AC-001, AC-002, {id: AC-003, text: Inline}, AC-004]\n" +
		"  - revision: " + plRevision + "\n    requirements: [{id: FR-001, text: T}]\n" +
		"  - title: Refund report\n    component: " + plComponent + "\n    requirements: [IF-001]\n" +
		"  - feature: ZEN-7\n    new_revision: true\n    scope_summary: S\n    requirements: [AC-005]\n" +
		"  - title: Brand new\n    component: " + plComponent + "\n    requirements: [{id: AC-006, text: New}]\n" +
		"---\n\n## Requirements\n\n| ID | Requirement |\n| --- | --- |\n| AC-001 | One |\n| AC-002 | Two |\n| IF-001 | Report |\n| AC-005 | Five |\n"
	srv, api := newFakeAPI(t, planPushRoutes(routeSet("GET /api/features", reply(200, featureList(
		featureRow(plFeatureR, "refund-export", "in_development", plRevision),
		featureRow(featureX, "refund-report", "planned", "bbbbbbbb-0000-4000-8000-0000000000a2"),
	)))))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file), "--dry-run", "--json"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if n := api.count("GET", "/api/features"); n != 1 {
		t.Fatalf("the feature list was read %d times, want once", n)
	}
	env, _ := decodePushResult(t, out.String())
	want := []string{
		"item 1: ZEN-42 already exists, so the texts of AC-001, AC-002, AC-003 are ignored; the plan records only requirement IDs and tags",
		"item 2: revision " + plRevision + " already exists, so the texts of FR-001 are ignored; the plan records only requirement IDs and tags",
		"item 3: reusing feature refund-report (" + featureX + ")",
		"item 3: feature refund-report already exists, so the texts of IF-001 are ignored; the plan records only requirement IDs and tags",
		"item 4: would create a new revision of ZEN-7",
		`item 5: would create feature "Brand new" (brand-new)`,
	}
	if strings.Join(env.Notes, "\n") != strings.Join(want, "\n") {
		t.Fatalf("notes:\n%s", strings.Join(env.Notes, "\n"))
	}
	items := env.Request["items"].([]any)
	if items[1].(map[string]any)["featureRevisionId"] != plRevision || items[2].(map[string]any)["featureId"] != featureX {
		t.Fatalf("items = %v", items)
	}
}

func TestPlanPush_ResolvesFeatureKeysInAnyCase(t *testing.T) {
	file := plHead + "  - feature: zen-42\n    requirements: [{id: AC-001, text: T}]\n---\n"
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, out := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file), "--dry-run", "--json"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if ref := api.only(t, "GET", "/api/features/resolve").Query.Get("ref"); ref != "ZEN-42" {
		t.Fatalf("resolve ref = %q, want ZEN-42", ref)
	}
	env, _ := decodePushResult(t, out.String())
	if len(env.Notes) != 1 || !strings.HasPrefix(env.Notes[0], "item 1: ZEN-42 already exists") || env.Request["items"].([]any)[0].(map[string]any)["featureId"] != plFeature42 {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestWriteJSON_ReportsValuesItCannotEncode(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, map[string]any{"c": make(chan int)}); err == nil || err.Error() != "json: unsupported type: chan int" || buf.Len() != 0 {
		t.Fatalf("writeJSON = %v, output %q", err, buf.String())
	}
}

func TestWritePlan_ReturnsWriteErrors(t *testing.T) {
	broken := errors.New("stdout closed")
	if err := writePlan(failingWriter{err: broken}, workPlanView{ID: plPlan}); !errors.Is(err, broken) {
		t.Fatalf("writePlan = %v", err)
	}
}

const (
	plLive   = "cccccccc-0000-4000-8000-0000000000a1"
	plMerged = "cccccccc-0000-4000-8000-0000000000a2"
)

func livePlanRow(id, status, sourcePath, hash string) map[string]any {
	return map[string]any{"id": id, "name": "Refunds", "status": status, "repository": plRepo, "source_ref": map[string]any{"content_hash": hash, "path": sourcePath}}
}

func planItem(revision, closedAt string) map[string]any {
	item := map[string]any{"feature_revision_id": revision, "closed_at": nil}
	if closedAt != "" {
		item["closed_at"] = closedAt
	}
	return item
}

func planWithItems(id string, items ...map[string]any) map[string]any {
	list := make([]any, 0, len(items))
	for _, it := range items {
		list = append(list, it)
	}
	return map[string]any{"id": id, "name": "Refunds", "status": "open", "items": list, "orders": []any{}}
}

func livePlanRoutes(list []any, detail map[string]any) map[string]func(recordedRequest) fakeReply {
	return map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + plProduct + "/work-plans": reply(200, map[string]any{"data": list, "total": len(list), "perPage": 100}),
		"GET /api/work-plans/" + plLive:                  reply(200, detail),
	}
}

func heldBy(item int, revision string) string {
	return "item " + strconv.Itoa(item) + ": revision " + revision + " is an open item of live plan " + plLive + ` ("Refunds", open); a revision sits in one live plan at a time, so abandon that plan first with ` + "`zensu plan abandon " + plLive + "`"
}

func withPushMode(file string, mode []string) []string {
	return append([]string{"push", file}, mode...)
}

func TestPlanPush_RefusesARevisionThatSitsInAnotherLivePlan(t *testing.T) {
	list := []any{livePlanRow(plMerged, "merged", "plan.md", "older"), livePlanRow(plLive, "open", "docs/plans/refunds.md", "other")}
	detail := planWithItems(plLive, planItem(plRev42, ""), planItem(plRevision, ""), planItem(plRev7, "2026-09-20T10:00:00Z"))
	cases := []struct {
		name    string
		file    string
		lookups []string
		want    string
	}{
		{"feature", plHead + "  - feature: ZEN-42\n---\n", []string{"GET /api/features/resolve"}, heldBy(1, plRev42)},
		{"open revision of a new_revision item", plHead + "  - feature: ZEN-42\n    new_revision: true\n    scope_summary: S\n---\n", []string{"GET /api/features/resolve"}, heldBy(1, plRev42)},
		{"feature reused by its slug", plHead + "  - title: Refund export\n    component: " + plComponent + "\n---\n", []string{"GET /api/features"}, heldBy(1, plRevision)},
		{"revision", plHead + "  - revision: " + plRevision + "\n---\n", []string{"GET /api/features"}, heldBy(1, plRevision)},
		{"every held item", plHead + "  - feature: ZEN-42\n  - feature: ZEN-7\n    new_revision: true\n    scope_summary: S\n  - revision: " + plRevision + "\n---\n", []string{"GET /api/features/resolve", "GET /api/features/resolve", "GET /api/features"}, heldBy(1, plRev42) + "; " + heldBy(3, plRevision)},
	}
	for _, tc := range cases {
		for _, mode := range [][]string{nil, {"--dry-run"}} {
			t.Run(strings.TrimSpace(tc.name+" "+strings.Join(mode, "")), func(t *testing.T) {
				srv, api := newFakeAPI(t, planPushRoutes(livePlanRoutes(list, detail)))
				f, out := loginFactory(srv)
				err := runCmd(t, NewPlanCmd(f), withPushMode(writePlanFile(t, tc.file), mode)...)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error = %v\nwant    %s", err, tc.want)
				}
				assertNoWrites(t, api)
				calls := append([]string{"GET /api/products/" + plProduct + "/work-plans", "GET /api/products/" + plProduct + "/repositories"}, tc.lookups...)
				assertCalls(t, api, append(calls, "GET /api/work-plans/"+plLive)...)
				if out.Len() != 0 {
					t.Fatalf("a refused push printed %q", out.String())
				}
			})
		}
	}
}

func TestPlanPush_PushesWhenNoLivePlanHoldsATargetRevision(t *testing.T) {
	list := []any{livePlanRow(plLive, "open", "docs/plans/refunds.md", "other")}
	cases := []struct {
		name   string
		file   string
		detail map[string]any
		reads  int
	}{
		{"closed item of the target revision", plHead + "  - feature: ZEN-42\n---\n", planWithItems(plLive, planItem(plRev42, "2026-09-20T10:00:00Z")), 1},
		{"new revision and new feature", plHead + "  - feature: ZEN-7\n    new_revision: true\n    scope_summary: S\n  - title: Refund webhook\n    component: " + plComponent + "\n---\n", planWithItems(plLive, planItem(plRev7, "")), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, planPushRoutes(livePlanRoutes(list, tc.detail)))
			f, _ := loginFactory(srv)
			if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, tc.file)); err != nil {
				t.Fatalf("plan push: %v", err)
			}
			if n := api.count("GET", "/api/work-plans/"+plLive); n != tc.reads {
				t.Fatalf("live plan read %d times, want %d: %v", n, tc.reads, api.summary())
			}
			if api.count("POST", "/api/products/"+plProduct+"/work-plans") != 1 {
				t.Fatalf("calls = %v", api.summary())
			}
		})
	}
}

func TestPlanPush_StopsWhenALivePlanCannotBeRead(t *testing.T) {
	list := []any{livePlanRow(plLive, "open", "docs/plans/refunds.md", "other")}
	cases := []struct {
		name  string
		reply fakeReply
		want  string
	}{
		{"server error", fakeReply{Status: 500, Body: map[string]any{"code": "internal", "message": "database unavailable"}}, "live plan " + plLive + ": "},
		{"invalid body", fakeReply{Status: 200, Body: "not json"}, "invalid work plan response: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			routes := livePlanRoutes(list, nil)
			routes["GET /api/work-plans/"+plLive] = func(recordedRequest) fakeReply { return tc.reply }
			srv, api := newFakeAPI(t, planPushRoutes(routes))
			f, _ := loginFactory(srv)
			err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, plHead+"  - feature: ZEN-42\n---\n"))
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to start with %q", err, tc.want)
			}
			assertNoWrites(t, api)
		})
	}
}

func TestPlanPush_NamesALivePlanOfAnEarlierVersionOfTheFile(t *testing.T) {
	list := []any{livePlanRow(plLive, "open", "plan.md", "older")}
	srv, api := newFakeAPI(t, planPushRoutes(livePlanRoutes(list, planWithItems(plLive, planItem(plRev42, "")))))
	edited := "live plan " + plLive + ` ("Refunds", open) was pushed from plan.md with other content, so this push re-pushes an edited file`

	f, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, plHead+"  - feature: ZEN-42\n---\n"), "--dry-run")
	if err == nil || err.Error() != heldBy(1, plRev42)+"; "+edited {
		t.Fatalf("error = %v", err)
	}

	revisionOnly := plHead + "  - revision: " + plRevision + "\n---\n"
	note := edited + "; abandon it with `zensu plan abandon " + plLive + "` if this push replaces it"
	f2, out2 := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, revisionOnly), "--dry-run", "--json"); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	env, _ := decodePushResult(t, out2.String())
	if len(env.Notes) != 1 || env.Notes[0] != note {
		t.Fatalf("notes = %q", env.Notes)
	}
	assertNoWrites(t, api)

	f3, out3 := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f3), "push", writePlanFile(t, revisionOnly)); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	if !strings.HasPrefix(out3.String(), note+"\nPushed plan "+plPlan) {
		t.Fatalf("output = %q", out3.String())
	}
}

const twoNewFeatures = plHead + "  - title: Refund audit\n    component: " + plComponent + "\n  - title: Refund report\n    component: " + plComponent + "\n  - feature: ZEN-42\n---\n"

func TestPlanPush_RefusesNewFeaturesBeyondTheAllowance(t *testing.T) {
	cases := []struct {
		name  string
		usage map[string]any
		want  string
	}{
		{"one feature too many", featureUsage(5, 4), "the push would create 2 feature(s), but the organization's plan allows 5 features and 4 exist already; nothing was created"},
		{"allowance used up", featureUsage(3, 3), "the push would create 2 feature(s), but the organization's plan allows 3 features and 3 exist already; nothing was created"},
	}
	for _, tc := range cases {
		for _, mode := range [][]string{nil, {"--dry-run"}} {
			t.Run(strings.TrimSpace(tc.name+" "+strings.Join(mode, "")), func(t *testing.T) {
				srv, api := newFakeAPI(t, planPushRoutes(routeSet("GET /api/billing/usage", reply(200, tc.usage))))
				f, out := loginFactory(srv)
				err := runCmd(t, NewPlanCmd(f), withPushMode(writePlanFile(t, twoNewFeatures), mode)...)
				if err == nil || err.Error() != tc.want {
					t.Fatalf("error = %v\nwant    %s", err, tc.want)
				}
				assertNoWrites(t, api)
				if api.count("GET", "/api/billing/usage") != 1 || out.Len() != 0 {
					t.Fatalf("calls %v, output %q", api.summary(), out.String())
				}
			})
		}
	}
}

func TestPlanPush_CreatesFeaturesWhenTheAllowanceAllowsThemOrCannotBeRead(t *testing.T) {
	cases := []struct {
		name  string
		usage fakeReply
	}{
		{"exactly at the allowance", fakeReply{Body: featureUsage(6, 4)}},
		{"unlimited", fakeReply{Body: featureUsage(-1, 400)}},
		{"no allowance in the answer", fakeReply{Body: map[string]any{"usage": map[string]any{"features": 400}}}},
		{"usage hidden from this login", fakeReply{Status: 403, Body: `{"code":"forbidden","message":"only admins read the usage"}`}},
		{"no billing on this deployment", fakeReply{Status: 404, Body: `{"code":"not_found","message":"not found"}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, planPushRoutes(routeSet("GET /api/billing/usage", func(recordedRequest) fakeReply { return tc.usage })))
			f, _ := loginFactory(srv)
			if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, twoNewFeatures)); err != nil {
				t.Fatalf("plan push: %v", err)
			}
			if api.count("GET", "/api/billing/usage") != 1 || api.count("POST", "/api/features") != 2 || api.count("POST", "/api/products/"+plProduct+"/work-plans") != 1 {
				t.Fatalf("calls = %v", api.summary())
			}
		})
	}
}

func TestPlanPush_ReadsTheAllowanceOnlyForNewFeatures(t *testing.T) {
	srv, api := newFakeAPI(t, planPushRoutes(routeSet("GET /api/billing/usage", reply(500, `{"code":"internal","message":"boom"}`))))
	f, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, plHead+"  - feature: ZEN-42\n  - revision: "+plRevision+"\n---\n")); err != nil {
		t.Fatalf("a push without new features must not depend on the allowance: %v", err)
	}
	if api.count("GET", "/api/billing/usage") != 0 {
		t.Fatalf("calls = %v", api.summary())
	}
	f2, _ := loginFactory(srv)
	err := runCmd(t, NewPlanCmd(f2), "push", writePlanFile(t, plHead+"  - title: Refund audit\n    component: "+plComponent+"\n---\n"))
	if err == nil || err.Error() != "feature allowance: boom (status 500)" {
		t.Fatalf("error = %v", err)
	}
	if api.count("GET", "/api/billing/usage") != 1 || api.count("POST", "/api/features") != 0 {
		t.Fatalf("calls = %v", api.summary())
	}
}

func TestPlanPush_LinksTheTaskOfAOneItemPlan(t *testing.T) {
	file := strings.Replace(plHead, "name: P\n", "name: P\ntask: "+plTask+"\n", 1) + "  - revision: " + plRevision + "\n---\n"
	srv, api := newFakeAPI(t, planPushRoutes(nil))
	f, _ := loginFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "push", writePlanFile(t, file)); err != nil {
		t.Fatalf("plan push: %v", err)
	}
	assertCalls(t, api,
		"GET /api/products/"+plProduct+"/work-plans",
		"GET /api/products/"+plProduct+"/repositories",
		"GET /api/tasks/"+plTask,
		"GET /api/features",
		"POST /api/products/"+plProduct+"/work-plans",
	)
	if body := api.only(t, "POST", "/api/products/"+plProduct+"/work-plans").Body; body["taskId"] != plTask || len(body["items"].([]any)) != 1 {
		t.Fatalf("plan body = %v", body)
	}
}

func TestPlanPush_RefusesPlanFileRulesBeforeAnyRequest(t *testing.T) {
	withTask := strings.Replace(plHead, "name: P\n", "name: P\ntask: "+plTask+"\n", 1)
	scope := "item 1: scope_summary belongs to new features (title) and new revisions (feature with new_revision: true); an existing revision keeps its scope"
	cases := []struct {
		name string
		file string
		want string
	}{
		{"scope_summary of an existing feature", plHead + "  - feature: ZEN-42\n    scope_summary: Refund partial captures\n---\n", scope},
		{"scope_summary of a revision", plHead + "  - revision: " + plRevision + "\n    scope_summary: S\n---\n", scope},
		{"task of a plan with several items", withTask + "  - feature: ZEN-42\n  - revision: " + plRevision + "\n---\n", "front matter: task links the work order of a one-item plan, and this plan has 2 items; drop task or push the item that implements the task as a plan of its own"},
	}
	for _, tc := range cases {
		for _, mode := range [][]string{nil, {"--dry-run"}} {
			t.Run(strings.TrimSpace(tc.name+" "+strings.Join(mode, "")), func(t *testing.T) {
				srv, api := newFakeAPI(t, planPushRoutes(nil))
				f, out := loginFactory(srv)
				path := writePlanFile(t, tc.file)
				err := runCmd(t, NewPlanCmd(f), withPushMode(path, mode)...)
				if err == nil || err.Error() != path+": "+tc.want {
					t.Fatalf("error = %v\nwant    %s: %s", err, path, tc.want)
				}
				if len(api.calls()) != 0 || out.Len() != 0 {
					t.Fatalf("calls %v, output %q", api.summary(), out.String())
				}
			})
		}
	}
}
