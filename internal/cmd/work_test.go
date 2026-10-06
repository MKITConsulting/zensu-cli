package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	woID           = "0f0f0f0f-1111-4222-8333-444444444444"
	woProduct      = "11111111-2222-4333-8444-555555555555"
	woPlan         = "22222222-3333-4444-8555-666666666666"
	woFeature      = "33333333-4444-4555-8666-777777777777"
	woQ            = "44444444-5555-4666-8777-888888888888"
	woRevisionID   = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	woTaskID       = "77777777-8888-4999-8aaa-bbbbbbbbbbbb"
	woAgentKey     = "88888888-9999-4aaa-8bbb-cccccccccccc"
	woAgentKey2    = "99999999-aaaa-4bbb-8ccc-dddddddddddd"
	woSessionID    = "sess-0001"
	woSessionToken = "zst_EXAMPLE_session_token"
)

func orderJSON(status string) map[string]any {
	return map[string]any{"id": woID, "work_plan_id": woPlan, "repository": "https://github.com/acme/app", "kind": "implement", "status": status, "attempt": 2}
}

func workClaimReply(retried bool) map[string]any {
	return map[string]any{"order": orderJSON("claimed"), "session_token": "zst_secret_token_value", "lease_seconds": 120, "retried": retried}
}

func workSessionFactory(srv *httptest.Server) (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	f := &Factory{
		Out: out,
		NewClient: func(context.Context) (*client.Client, error) {
			return client.New(&config.Config{AccessToken: woSessionToken}, srv.URL, "", client.WithHTTPClient(srv.Client())), nil
		},
	}
	return f, out
}

func workFailingSessionFactory() *Factory {
	return &Factory{
		Out: &bytes.Buffer{},
		NewClient: func(context.Context) (*client.Client, error) {
			return client.New(&config.Config{AccessToken: woSessionToken}, "http://zensu.test", "", client.WithHTTPClient(&http.Client{Transport: failingRoundTripper{}})), nil
		},
	}
}

func workTimeoutFactory(srv *httptest.Server, timeout time.Duration) (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	f := &Factory{
		Out: out,
		NewClient: func(context.Context) (*client.Client, error) {
			return client.New(&config.Config{APIKey: "zsk_test"}, srv.URL, "", client.WithHTTPClient(&http.Client{Timeout: timeout})), nil
		},
	}
	return f, out
}

func setWorkClaimTimeoutMargin(t *testing.T, d time.Duration) {
	t.Helper()
	previous := workClaimTimeoutMargin
	workClaimTimeoutMargin = d
	t.Cleanup(func() { workClaimTimeoutMargin = previous })
}

func assertWorkColumnsAligned(t *testing.T, table string, columns map[string]string) {
	t.Helper()
	lines := strings.Split(table, "\n")
	if len(lines) < 2 {
		t.Fatalf("table = %q", table)
	}
	for label, value := range columns {
		want, got := strings.Index(lines[0], label), strings.Index(lines[1], value)
		if want < 0 || got != want {
			t.Errorf("column %s starts at %d but its value %q at %d:\n%s", label, want, value, got, table)
		}
	}
}

func TestWorkCmd_HelpNamesTheCredentialOfEveryVerb(t *testing.T) {
	work := NewWorkCmd(&Factory{Out: &bytes.Buffer{}})
	for _, want := range []string{
		"Human verbs (create, approve, requeue, cancel, confirm-merge, answer, overturn, policy set,\nrepositories)",
		"Worker verbs (claim, confirm, release, usage) need an agent key and refuse to run while\nZENSU_SESSION_TOKEN is set.",
		"Session verbs (heartbeat, event, ask, followup) authenticate\nwith ZENSU_SESSION_TOKEN, which overrides any stored login, and refuse to run without it.",
	} {
		if !strings.Contains(work.Long, want) {
			t.Errorf("work help misses %q:\n%s", want, work.Long)
		}
	}
	claim, _, err := work.Find([]string{"claim"})
	if err != nil || !strings.Contains(claim.Long, "An agent key claims only in products whose automation policy allows it: zensu work policy set --product <product id> --add-allowed-key <key id>.") {
		t.Fatalf("claim help = %q (%v)", claim.Long, err)
	}
	overturn, _, err := work.Find([]string{"overturn"})
	if err != nil || overturn.Flags().Lookup("plan-wide") != nil {
		t.Fatalf("overturn must not offer --plan-wide (%v)", err)
	}
}

func TestWorkCreate_ResolvesTheFeatureKeyAndCreatesADraftFromTheCLI(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/features/resolve": reply(200, map[string]any{"id": woFeature}),
		"POST /api/products/" + woProduct + "/work-orders": reply(201, map[string]any{
			"id": woPlan, "status": "proposed", "created": true, "orders": []any{orderJSON("draft")},
		}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "create", "--product", woProduct, "--feature", "ZEN-42", "--repository", "https://github.com/acme/app", "--base-branch", "develop", "--harness", "claude", "--task", woTaskID); err != nil {
		t.Fatalf("work create: %v", err)
	}
	if got := api.only(t, "GET", "/api/features/resolve").Query.Get("ref"); got != "ZEN-42" {
		t.Fatalf("resolve ref = %q", got)
	}
	body := api.only(t, "POST", "/api/products/"+woProduct+"/work-orders").Body
	want := map[string]any{"featureId": woFeature, "repository": "https://github.com/acme/app", "baseBranch": "develop", "preferredHarness": "claude", "taskId": woTaskID, "createdVia": "cli"}
	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %v, want %v", k, body[k], v)
		}
	}
	if _, ok := body["revisionId"]; ok {
		t.Error("body must not name a revision")
	}
	if got := out.String(); got != "Created plan "+woPlan+" with work order "+woID+" (draft)\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestWorkCreate_ResolvesALowerCaseKeyByItsCanonicalForm(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/features/resolve": reply(200, map[string]any{"id": woFeature}),
		"POST /api/products/" + woProduct + "/work-orders": reply(201, map[string]any{
			"id": woPlan, "created": true, "orders": []any{orderJSON("draft")},
		}),
	})
	f, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "create", "--product", woProduct, "--feature", "zen-42", "--repository", "https://github.com/acme/app"); err != nil {
		t.Fatalf("work create: %v", err)
	}
	if got := api.only(t, "GET", "/api/features/resolve").Query.Get("ref"); got != "ZEN-42" {
		t.Fatalf("resolve ref = %q, want the canonical key ZEN-42", got)
	}
	if body := api.only(t, "POST", "/api/products/"+woProduct+"/work-orders").Body; body["featureId"] != woFeature {
		t.Fatalf("body = %v", body)
	}
}

func TestWorkCreate_RevisionAndReusedPlan(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/products/" + woProduct + "/work-orders": reply(200, map[string]any{"id": woPlan, "created": false, "orders": []any{orderJSON("queued")}}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "create", "--product", woProduct, "--revision", woRevisionID, "--repository", "https://github.com/acme/app"); err != nil {
		t.Fatalf("work create: %v", err)
	}
	body := api.only(t, "POST", "/api/products/"+woProduct+"/work-orders").Body
	if body["revisionId"] != woRevisionID || body["featureId"] != nil || body["baseBranch"] != nil {
		t.Fatalf("body = %v", body)
	}
	if !strings.HasPrefix(out.String(), "Reused live plan "+woPlan) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkCreate_JSONAndUUIDFeaturePassThrough(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/products/" + woProduct + "/work-orders": reply(201, `{"id":"`+woPlan+`","created":true,"orders":[]}`),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "create", "--product", woProduct, "--feature", woFeature, "--repository", "r", "--json"); err != nil {
		t.Fatalf("work create: %v", err)
	}
	if api.count("GET", "/api/features/resolve") != 0 {
		t.Fatal("a UUID must not be resolved")
	}
	if !strings.Contains(out.String(), `"id": "`+woPlan+`"`) {
		t.Fatalf("json output = %q", out.String())
	}
}

func TestWorkCreate_Validation(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	cases := [][]string{
		{"create", "--repository", "r", "--feature", "ZEN-1"},
		{"create", "--product", woProduct, "--feature", "ZEN-1"},
		{"create", "--product", woProduct, "--repository", "r"},
		{"create", "--product", woProduct, "--repository", "r", "--feature", "ZEN-1", "--revision", "x"},
	}
	for _, args := range cases {
		f, _ := testFactory(srv)
		err := runCmd(t, NewWorkCmd(f), args...)
		if err == nil || !strings.Contains(err.Error(), "exactly one of --feature or --revision") {
			t.Errorf("%v: error = %v", args, err)
		}
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("validation errors sent %d requests", n)
	}
}

func TestWorkCommands_RejectIDFlagsThatAreNotUUIDs(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"create", "--product", "acme", "--revision", woRevisionID, "--repository", "r"}, `--product must be a UUID, got "acme"`},
		{[]string{"create", "--product", woProduct, "--revision", "rev-1", "--repository", "r"}, `--revision must be a UUID, got "rev-1"`},
		{[]string{"create", "--product", woProduct, "--revision", woRevisionID, "--repository", "r", "--task", "t-1"}, `--task must be a UUID, got "t-1"`},
		{[]string{"create", "--product", woProduct, "--feature", "zen_42", "--repository", "r"}, `--feature must be a KEY-N reference or a UUID, got "zen_42"`},
		{[]string{"list", "--product", "acme"}, `--product must be a UUID, got "acme"`},
		{[]string{"list", "--plan", "plan-1"}, `--plan must be a UUID, got "plan-1"`},
		{[]string{"list", "--feature", "42"}, `--feature must be a KEY-N reference or a UUID, got "42"`},
		{[]string{"claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app", "--product", "acme"}, `--product must be a UUID, got "acme"`},
		{[]string{"questions", "--plan", "plan-1"}, `--plan must be a UUID, got "plan-1"`},
		{[]string{"questions", "--plan", woPlan, "--order", "order-1"}, `--order must be a UUID, got "order-1"`},
		{[]string{"policy", "get", "--product", "acme"}, `--product must be a UUID, got "acme"`},
		{[]string{"policy", "set", "--product", "acme"}, `--product must be a UUID, got "acme"`},
		{[]string{"policy", "set", "--product", woProduct, "--allowed-key", woAgentKey, "--allowed-key", "key-2"}, `--allowed-key must be a UUID, got "key-2"`},
		{[]string{"policy", "set", "--product", woProduct, "--add-allowed-key", woAgentKey, "--add-allowed-key", "key-2"}, `--add-allowed-key must be a UUID, got "key-2"`},
		{[]string{"policy", "set", "--product", woProduct, "--remove-allowed-key", "key-3"}, `--remove-allowed-key must be a UUID, got "key-3"`},
		{[]string{"repositories", "list", "--product", "acme"}, `--product must be a UUID, got "acme"`},
		{[]string{"repositories", "add", "--product", "acme", "--repository", "https://github.com/acme/app"}, `--product must be a UUID, got "acme"`},
		{[]string{"repositories", "update", "r1", "--product", "acme", "--clear"}, `--product must be a UUID, got "acme"`},
		{[]string{"repositories", "remove", "r1", "--product", "acme"}, `--product must be a UUID, got "acme"`},
	} {
		f, _ := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), tc.args...); err == nil || err.Error() != tc.want {
			t.Errorf("%v: error = %v, want %q", tc.args, err, tc.want)
		}
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("invalid IDs sent %d requests: %v", n, api.summary())
	}
}

func TestWorkCreate_ResolveFailureStopsBeforeTheWrite(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/features/resolve": reply(404, `{"code":"not_found","message":"no feature ZEN-9"}`),
	})
	f, _ := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "create", "--product", woProduct, "--feature", "ZEN-9", "--repository", "r")
	if err == nil || !strings.Contains(err.Error(), "resolve ZEN-9: no feature ZEN-9 (status 404)") {
		t.Fatalf("error = %v", err)
	}
	if api.count("POST", "/api/products/"+woProduct+"/work-orders") != 0 {
		t.Fatal("no order may be created after a failed resolve")
	}
}

func TestWorkList_FiltersAndTable(t *testing.T) {
	blocked := orderJSON("blocked")
	blocked["blocked_kind"] = "question"
	blocked["pr_url"] = "https://github.com/acme/app/pull/7"
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/features/resolve": reply(200, map[string]any{"id": woFeature}),
		"GET /api/work-orders":      reply(200, map[string]any{"data": []any{blocked}, "total": 3}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "list", "--product", woProduct, "--plan", woPlan, "--feature", "ZEN-3", "--status", "blocked", "--page", "2", "--per-page", "5"); err != nil {
		t.Fatalf("work list: %v", err)
	}
	q := api.only(t, "GET", "/api/work-orders").Query
	for k, v := range map[string]string{"product": woProduct, "plan": woPlan, "feature": woFeature, "status": "blocked", "page": "2", "per_page": "5"} {
		if q.Get(k) != v {
			t.Errorf("query %s = %q, want %q", k, q.Get(k), v)
		}
	}
	for _, want := range []string{"ID", "STATUS", woID, "blocked(question)", "https://github.com/acme/app/pull/7", "1 of 3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table misses %q:\n%s", want, out.String())
		}
	}
	assertWorkColumnsAligned(t, out.String(), map[string]string{"STATUS": "blocked(question)", "REPOSITORY": "https://github.com/acme/app", "PR": "https://github.com/acme/app/pull/7"})
}

func TestWorkList_JSONWithoutFilters(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-orders": reply(200, `{"data":[],"total":0}`),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "list", "--json"); err != nil {
		t.Fatalf("work list: %v", err)
	}
	if len(api.only(t, "GET", "/api/work-orders").Query) != 0 {
		t.Fatal("no filter means no query")
	}
	if !strings.Contains(out.String(), `"total": 0`) {
		t.Fatalf("json output = %q", out.String())
	}
}

func TestWorkGetAndEvents(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-orders/" + woID: reply(200, map[string]any{"id": woID, "status": "implementing", "spec": map[string]any{"title": "x"}}),
		"GET /api/work-orders/" + woID + "/events": reply(200, map[string]any{"data": []any{
			map[string]any{"event_type": "stage", "from_status": "claimed", "to_status": "implementing", "actor_kind": "session", "attempt": 2, "occurred_at": "2026-09-28T10:00:00Z"},
			map[string]any{"event_type": "created", "actor_kind": "user", "occurred_at": "2026-09-28T09:00:00Z"},
		}, "total": 2}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "get", woID, "--json"); err != nil {
		t.Fatalf("work get: %v", err)
	}
	if !strings.Contains(out.String(), `"status": "implementing"`) {
		t.Fatalf("get output = %q", out.String())
	}
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "events", woID, "--page", "1", "--per-page", "50"); err != nil {
		t.Fatalf("work events: %v", err)
	}
	q := api.only(t, "GET", "/api/work-orders/"+woID+"/events").Query
	if q.Get("page") != "1" || q.Get("per_page") != "50" {
		t.Fatalf("events query = %v", q)
	}
	for _, want := range []string{"OCCURRED", "stage", "claimed", "implementing", "session", "created", "2 of 2"} {
		if !strings.Contains(out2.String(), want) {
			t.Errorf("events table misses %q:\n%s", want, out2.String())
		}
	}
	assertWorkColumnsAligned(t, out2.String(), map[string]string{"TYPE": "stage", "FROM": "claimed", "ACTOR": "session"})
}

func TestWorkEvents_JSON(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-orders/" + woID + "/events": reply(200, `{"data":[],"total":0}`),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "events", woID, "--json"); err != nil {
		t.Fatalf("work events: %v", err)
	}
	if !strings.Contains(out.String(), `"data": []`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkHumanDecisions(t *testing.T) {
	cases := []struct {
		args     []string
		path     string
		body     map[string]any
		response map[string]any
		want     string
	}{
		{[]string{"approve", woID}, "/approve", nil, orderJSON("queued"), "Approved work order " + woID + ": queued\n"},
		{[]string{"requeue", woID}, "/requeue", nil, orderJSON("queued"), "Requeued work order " + woID + ": queued\n"},
		{[]string{"cancel", woID, "--confirm-pr-closed"}, "/cancel", map[string]any{"confirmPrClosed": true}, orderJSON("cancelled"), "Cancelled work order " + woID + ": cancelled\n"},
		{[]string{"cancel", woID}, "/cancel", map[string]any{"confirmPrClosed": false}, orderJSON("cancelled"), "Cancelled work order " + woID + ": cancelled\n"},
		{[]string{"confirm-merge", woID, "--merge-sha", "abc1234", "--head-sha", strings.Repeat("b", 40)}, "/confirm-merge", map[string]any{"mergeSha": "abc1234", "headSha": strings.Repeat("b", 40)}, orderJSON("merged"), "Confirmed merge of work order " + woID + ": merged\n"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args[:1], " ")+tc.path, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"POST /api/work-orders/" + woID + tc.path: reply(200, tc.response),
			})
			f, out := testFactory(srv)
			if err := runCmd(t, NewWorkCmd(f), tc.args...); err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			got := api.only(t, "POST", "/api/work-orders/"+woID+tc.path)
			if tc.body == nil && got.Raw != "" {
				t.Fatalf("body = %q, want none", got.Raw)
			}
			for k, v := range tc.body {
				if got.Body[k] != v {
					t.Errorf("body[%s] = %v, want %v", k, got.Body[k], v)
				}
			}
			if out.String() != tc.want {
				t.Fatalf("output = %q, want %q", out.String(), tc.want)
			}
		})
	}
}

func TestWorkHumanDecisions_JSONAndBlockedKind(t *testing.T) {
	blocked := orderJSON("blocked")
	blocked["blocked_kind"] = "plan_approval"
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/requeue":       reply(200, blocked),
		"POST /api/work-orders/" + woID + "/approve":       reply(200, orderJSON("queued")),
		"POST /api/work-orders/" + woID + "/cancel":        reply(200, orderJSON("cancelled")),
		"POST /api/work-orders/" + woID + "/confirm-merge": reply(200, orderJSON("merged")),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "requeue", woID); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	if out.String() != "Requeued work order "+woID+": blocked (plan_approval)\n" {
		t.Fatalf("output = %q", out.String())
	}
	for _, args := range [][]string{{"approve", woID, "--json"}, {"cancel", woID, "--json"}, {"confirm-merge", woID, "--merge-sha", "a", "--head-sha", "b", "--json"}} {
		f, out := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out.String(), `"id": "`+woID+`"`) {
			t.Fatalf("%v output = %q", args, out.String())
		}
	}
}

func TestWorkHumanDecisions_SurfaceTheInteractiveLoginRefusal(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/approve": reply(403, `{"code":"interactive_user_required","message":"this decision needs a signed-in person; API keys cannot make it"}`),
	})
	f, out := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "approve", woID)
	if err == nil || err.Error() != "this decision needs a signed-in person; API keys cannot make it (status 403)" {
		t.Fatalf("error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkConfirmMerge_SendsTheMergeSHAOnlyWhenGiven(t *testing.T) {
	head := strings.Repeat("b", 40)
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/confirm-merge": reply(200, orderJSON("merged")),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "confirm-merge", woID, "--head-sha", head); err != nil {
		t.Fatalf("confirm-merge: %v", err)
	}
	if raw := api.only(t, "POST", "/api/work-orders/"+woID+"/confirm-merge").Raw; raw != `{"headSha":"`+head+`"}` {
		t.Fatalf("body = %s, want only the head SHA", raw)
	}
	if out.String() != "Confirmed merge of work order "+woID+": merged\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkConfirmMerge_RequiresTheHeadSHA(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	f, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "confirm-merge", woID, "--merge-sha", "abc1234"); err == nil || err.Error() != "--head-sha is required" {
		t.Fatalf("error = %v", err)
	}
	if len(api.calls()) != 0 {
		t.Fatal("no request without the head SHA")
	}
}

func TestWorkClaim(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/claim": reply(200, workClaimReply(false)),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app", "--repository", "https://github.com/acme/lib", "--wait", "25", "--product", woProduct); err != nil {
		t.Fatalf("claim: %v", err)
	}
	body := api.only(t, "POST", "/api/work-orders/claim").Body
	if body["clientSessionId"] != woSessionID || body["waitSeconds"] != float64(25) || body["productId"] != woProduct || body["clientName"] != "zensu-cli" || body["clientVersion"] != "dev" {
		t.Fatalf("body = %v", body)
	}
	if kinds := body["kinds"].([]any); len(kinds) != 1 || kinds[0] != "implement" {
		t.Fatalf("kinds = %v", body["kinds"])
	}
	if repos := body["repositories"].([]any); len(repos) != 2 || repos[1] != "https://github.com/acme/lib" {
		t.Fatalf("repositories = %v", body["repositories"])
	}
	if strings.Contains(out.String(), "zst_secret") {
		t.Fatal("the text output must not print the session token")
	}
	if !strings.Contains(out.String(), "Claimed work order "+woID+" attempt 2 for 120 s") {
		t.Fatalf("output = %q", out.String())
	}

	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app", "--kind", "implement,tests", "--json"); err != nil {
		t.Fatalf("claim json: %v", err)
	}
	if !strings.Contains(out2.String(), `"session_token": "zst_secret_token_value"`) {
		t.Fatalf("--json must carry the token: %q", out2.String())
	}
	calls := api.calls()
	last := calls[len(calls)-1].Body
	if kinds := last["kinds"].([]any); len(kinds) != 2 || kinds[1] != "tests" {
		t.Fatalf("kinds = %v", last["kinds"])
	}
	if _, ok := last["productId"]; ok {
		t.Fatal("productId must be omitted without --product")
	}
}

func TestWorkClaim_NothingClaimable(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/claim": reply(204, nil),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if out.String() != "No work order became claimable. An agent key claims only in products whose automation policy allows it: zensu work policy set --product <product id> --add-allowed-key <key id>.\n" {
		t.Fatalf("output = %q", out.String())
	}
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app", "--json"); err != nil {
		t.Fatalf("claim json: %v", err)
	}
	if out2.String() != "{\n  \"order\": null\n}\n" {
		t.Fatalf("json output = %q", out2.String())
	}
}

func TestWorkClaim_RequiresSessionID(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	f, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "claim"); err == nil || err.Error() != "--session-id is required" {
		t.Fatalf("error = %v", err)
	}
	if len(api.calls()) != 0 {
		t.Fatal("no request without a session id")
	}
}

func TestWorkClaim_ValidatesItsFlagsLocally(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	repo := "https://github.com/acme/app"
	long := strings.Repeat("s", 129)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"claim", "--session-id", "short", "--repository", repo}, `--session-id must be 8 to 128 characters of letters, digits, dot, underscore, colon or dash, got "short"`},
		{[]string{"claim", "--session-id", "sess/0001", "--repository", repo}, `--session-id must be 8 to 128 characters of letters, digits, dot, underscore, colon or dash, got "sess/0001"`},
		{[]string{"claim", "--session-id", long, "--repository", repo}, `--session-id must be 8 to 128 characters of letters, digits, dot, underscore, colon or dash, got "` + long + `"`},
		{[]string{"claim", "--session-id", woSessionID}, "--repository is required; name every repository this worker can check out"},
		{[]string{"claim", "--session-id", woSessionID, "--repository", repo, "--wait", "51"}, "--wait must lie between 0 and 50 seconds"},
		{[]string{"claim", "--session-id", woSessionID, "--repository", repo, "--wait=-1"}, "--wait must lie between 0 and 50 seconds"},
	} {
		f, _ := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), tc.args...); err == nil || err.Error() != tc.want {
			t.Errorf("%v: error = %v, want %q", tc.args, err, tc.want)
		}
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("invalid claims sent %d requests", n)
	}
}

func TestWorkClaimTimeout_AddsFifteenSecondsToTheWait(t *testing.T) {
	for wait, want := range map[int]time.Duration{0: 15 * time.Second, 25: 40 * time.Second, 50: 65 * time.Second} {
		if got := workClaimTimeout(wait); got != want {
			t.Errorf("workClaimTimeout(%d) = %v, want %v", wait, got, want)
		}
	}
}

func TestWorkClaim_RunsOnAClientWhoseTimeoutCoversTheWait(t *testing.T) {
	setWorkClaimTimeoutMargin(t, 2*time.Second)
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/claim": func(recordedRequest) fakeReply {
			time.Sleep(300 * time.Millisecond)
			return fakeReply{Status: http.StatusOK, Body: workClaimReply(false)}
		},
	})
	f, out := workTimeoutFactory(srv, 100*time.Millisecond)
	if err := runCmd(t, NewWorkCmd(f), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app", "--wait", "1"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if n := api.count("POST", "/api/work-orders/claim"); n != 1 {
		t.Fatalf("claims = %d, want 1", n)
	}
	if !strings.HasPrefix(out.String(), "Claimed work order "+woID+" attempt 2 for 120 s") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkClaim_RetriesATimedOutClaimOnceWithTheSameBody(t *testing.T) {
	setWorkClaimTimeoutMargin(t, 300*time.Millisecond)
	release := make(chan struct{})
	var mu sync.Mutex
	attempts := 0
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/claim": func(recordedRequest) fakeReply {
			mu.Lock()
			attempts++
			first := attempts == 1
			mu.Unlock()
			if first {
				<-release
			}
			return fakeReply{Status: http.StatusOK, Body: workClaimReply(true)}
		},
	})
	t.Cleanup(func() { close(release) })
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app"); err != nil {
		t.Fatalf("claim: %v", err)
	}
	calls := api.calls()
	if len(calls) != 2 || calls[0].Raw != calls[1].Raw || calls[1].Body["clientSessionId"] != woSessionID {
		t.Fatalf("claims = %v, want the timed-out claim retried once with the identical body", api.summary())
	}
	if out.String() != "Claimed work order "+woID+" attempt 2 for 120 s; confirm it once the session started. Use --json to read the session token.\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkClaim_StopsAfterTheSecondTimeout(t *testing.T) {
	setWorkClaimTimeoutMargin(t, 200*time.Millisecond)
	release := make(chan struct{})
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/claim": func(recordedRequest) fakeReply {
			<-release
			return fakeReply{Status: http.StatusNoContent}
		},
	})
	t.Cleanup(func() { close(release) })
	f, out := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app")
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("error = %v, want the client timeout", err)
	}
	if n := api.count("POST", "/api/work-orders/claim"); n != 2 || out.Len() != 0 {
		t.Fatalf("claims = %d output = %q, want exactly one retry and no output", n, out.String())
	}
}

func TestWorkClaim_DoesNotRetryAServerError(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/claim": reply(503, `{"code":"draining","message":"the server is shutting down; retry after the advertised delay"}`),
	})
	f, _ := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app")
	if err == nil || err.Error() != "the server is shutting down; retry after the advertised delay (status 503)" {
		t.Fatalf("error = %v", err)
	}
	if n := api.count("POST", "/api/work-orders/claim"); n != 1 {
		t.Fatalf("claims = %d, want no retry after a server error", n)
	}
}

func TestWorkConfirmReleaseUsage(t *testing.T) {
	lease := map[string]any{"order": orderJSON("claimed"), "lease_seconds": 1800, "answers": []any{}}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/confirm": reply(200, lease),
		"POST /api/work-orders/" + woID + "/release": reply(200, orderJSON("queued")),
		"POST /api/work-orders/" + woID + "/events":  reply(201, map[string]any{"event": map[string]any{"id": "e"}, "order": orderJSON("implementing"), "replay": false}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "confirm", woID, "--session-id", woSessionID, "--attempt", "2"); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if b := api.only(t, "POST", "/api/work-orders/"+woID+"/confirm").Body; b["clientSessionId"] != woSessionID || b["attempt"] != float64(2) {
		t.Fatalf("confirm body = %v", b)
	}
	if out.String() != "Confirmed work order "+woID+": claimed, lease 1800 s\n" {
		t.Fatalf("confirm output = %q", out.String())
	}

	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "release", woID, "--session-id", woSessionID, "--attempt", "2", "--outcome", "rate_limited", "--not-before", "2026-09-28T12:00:00+02:00", "--reason", "Rate limit of the account."); err != nil {
		t.Fatalf("release: %v", err)
	}
	b := api.only(t, "POST", "/api/work-orders/"+woID+"/release").Body
	if b["outcome"] != "rate_limited" || b["notBefore"] != "2026-09-28T10:00:00Z" || b["reason"] != "Rate limit of the account." || b["attempt"] != float64(2) {
		t.Fatalf("release body = %v", b)
	}
	if out2.String() != "Released work order "+woID+": queued\n" {
		t.Fatalf("release output = %q", out2.String())
	}

	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f3), "usage", woID, "--session-id", woSessionID, "--attempt", "2", "--cost-usd", "1.25", "--turns", "40", "--duration-seconds", "900", "--input-tokens", "1000", "--output-tokens", "200", "--client-event-id", "usage-000001"); err != nil {
		t.Fatalf("usage: %v", err)
	}
	u := api.only(t, "POST", "/api/work-orders/"+woID+"/events").Body
	if u["type"] != "usage" || u["clientEventId"] != "usage-000001" || u["clientSessionId"] != woSessionID || u["attempt"] != float64(2) {
		t.Fatalf("usage body = %v", u)
	}
	d := u["detail"].(map[string]any)
	if d["costUsd"] != 1.25 || d["turns"] != float64(40) || d["durationSeconds"] != float64(900) || d["inputTokens"] != float64(1000) || d["outputTokens"] != float64(200) {
		t.Fatalf("usage detail = %v", d)
	}
	if out3.String() != "Recorded usage for work order "+woID+" (event usage-000001)\n" {
		t.Fatalf("usage output = %q", out3.String())
	}
}

func TestWorkUsage_SendsOnlyGivenCountersAndJSON(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/events": reply(201, `{"event":{"id":"e"},"order":{"id":"x"},"replay":false}`),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "usage", woID, "--session-id", woSessionID, "--attempt", "1", "--turns", "3", "--json"); err != nil {
		t.Fatalf("usage: %v", err)
	}
	d := api.only(t, "POST", "/api/work-orders/"+woID+"/events").Body["detail"].(map[string]any)
	if len(d) != 1 || d["turns"] != float64(3) {
		t.Fatalf("detail = %v", d)
	}
	if !strings.Contains(out.String(), `"replay": false`) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkUsage_RefusesACostThatIsNotANumber(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	f, out := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "usage", woID, "--session-id", woSessionID, "--attempt", "1", "--cost-usd", "NaN")
	if err == nil || err.Error() != "json: unsupported value: NaN" {
		t.Fatalf("error = %v", err)
	}
	if len(api.calls()) != 0 || out.Len() != 0 {
		t.Fatalf("calls = %v output = %q, want nothing sent", api.summary(), out.String())
	}
}

func TestWorkWorkerVerbs_Validation(t *testing.T) {
	_, parseErr := time.Parse(time.RFC3339, "tomorrow")
	srv, api := newFakeAPI(t, nil)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"confirm", woID}, "--session-id and --attempt are required"},
		{[]string{"confirm", woID, "--session-id", "s", "--attempt", "1"}, `--session-id must be 8 to 128 characters of letters, digits, dot, underscore, colon or dash, got "s"`},
		{[]string{"release", woID, "--session-id", woSessionID, "--attempt", "1"}, "--outcome is required (success|failure|rate_limited|runtime_cap|interrupted)"},
		{[]string{"release", woID, "--session-id", woSessionID, "--attempt", "1", "--outcome", "rate_limited"}, "--outcome rate_limited needs --not-before, the RFC 3339 time before which the order must not be claimed again"},
		{[]string{"release", woID, "--session-id", woSessionID, "--attempt", "1", "--outcome", "failure", "--not-before", "2026-09-28T12:00:00Z"}, "--not-before applies only to --outcome rate_limited"},
		{[]string{"release", woID, "--session-id", woSessionID, "--attempt", "1", "--outcome", "rate_limited", "--not-before", "tomorrow"}, "--not-before must be an RFC 3339 timestamp: " + parseErr.Error()},
		{[]string{"usage", woID, "--session-id", "s"}, "--session-id and --attempt are required"},
		{[]string{"usage", woID, "--session-id", "bad id!!", "--attempt", "1"}, `--session-id must be 8 to 128 characters of letters, digits, dot, underscore, colon or dash, got "bad id!!"`},
	} {
		f, _ := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), tc.args...); err == nil || err.Error() != tc.want {
			t.Errorf("%v: error = %v, want %q", tc.args, err, tc.want)
		}
	}
	if len(api.calls()) != 0 {
		t.Fatal("validation errors must not reach the server")
	}
}

func TestWorkVerbs_RefuseTheWrongCredentialBeforeAnyRequest(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	worker := "needs the agent key; unset ZENSU_SESSION_TOKEN, which replaces the stored login"
	session := "runs inside a work order session; set ZENSU_SESSION_TOKEN to the session token of its claim"
	for _, tc := range []struct {
		args    []string
		session bool
		want    string
	}{
		{[]string{"claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app"}, true, "zensu work claim " + worker},
		{[]string{"confirm", woID, "--session-id", woSessionID, "--attempt", "1"}, true, "zensu work confirm " + worker},
		{[]string{"release", woID, "--session-id", woSessionID, "--attempt", "1", "--outcome", "success"}, true, "zensu work release " + worker},
		{[]string{"usage", woID, "--session-id", woSessionID, "--attempt", "1", "--turns", "1"}, true, "zensu work usage " + worker},
		{[]string{"heartbeat", woID}, false, "zensu work heartbeat " + session},
		{[]string{"event", woID, "--stage", "pr_open"}, false, "zensu work event " + session},
		{[]string{"ask", woID, "--category", "scope", "--question", "Which locale?", "--blocking"}, false, "zensu work ask " + session},
		{[]string{"followup", woID, "--title", "t", "--rationale", "r", "--severity", "low"}, false, "zensu work followup " + session},
	} {
		f, out := testFactory(srv)
		if tc.session {
			f, out = workSessionFactory(srv)
		}
		err := runCmd(t, NewWorkCmd(f), tc.args...)
		if err == nil || err.Error() != tc.want || out.Len() != 0 {
			t.Errorf("%v: error = %v output = %q, want %q", tc.args, err, out.String(), tc.want)
		}
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("a refused credential sent %d requests: %v", n, api.summary())
	}
}

func TestWorkVerbs_SurfaceTheClientError(t *testing.T) {
	f := &Factory{Out: &bytes.Buffer{}, NewClient: func(context.Context) (*client.Client, error) { return nil, errors.New("config unreadable") }}
	if err := runCmd(t, NewWorkCmd(f), "heartbeat", woID); err == nil || err.Error() != "config unreadable" {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkHeartbeat(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/heartbeat": reply(200, map[string]any{"order": orderJSON("implementing"), "lease_seconds": 1800, "head_sha": nil, "answers": []any{map[string]any{"id": woQ}}}),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "heartbeat", woID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got := api.only(t, "POST", "/api/work-orders/"+woID+"/heartbeat")
	if got.Raw != "" || got.Auth != "Bearer "+woSessionToken || got.APIKey != "" {
		t.Fatalf("heartbeat body = %q auth = %q key = %q, want no body and the session token", got.Raw, got.Auth, got.APIKey)
	}
	if out.String() != "Renewed work order "+woID+": implementing, lease 1800 s, 1 answered question(s)\n" {
		t.Fatalf("output = %q", out.String())
	}
	f2, out2 := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "heartbeat", woID, "--json"); err != nil {
		t.Fatalf("heartbeat json: %v", err)
	}
	if !strings.Contains(out2.String(), `"lease_seconds": 1800`) {
		t.Fatalf("json output = %q", out2.String())
	}
}

func TestWorkHeartbeat_StaleAttemptIsAnError(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/heartbeat": reply(409, `{"code":"stale_attempt","message":"the order is held by another attempt or its lease expired; stop without further forge writes"}`),
	})
	f, _ := workSessionFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "heartbeat", woID)
	if err == nil || !strings.Contains(err.Error(), "stop without further forge writes (status 409)") {
		t.Fatalf("error = %v", err)
	}
}

var generatedEventID = regexp.MustCompile(`^cli-[0-9a-f]{24}$`)

func TestWorkEvent_StageArtifactBlocked(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/events": reply(201, map[string]any{"event": map[string]any{"id": "e"}, "order": orderJSON("pr_open"), "replay": false}),
	})
	f, out := workSessionFactory(srv)
	head := strings.Repeat("c", 40)
	if err := runCmd(t, NewWorkCmd(f), "event", woID, "--stage", "pr_open", "--pr-url", "https://github.com/acme/app/pull/9", "--pr-number", "9", "--branch", "wo-abc", "--head", head, "--autopilot-stage", "PR_OPEN", "--autopilot-run-id", "run-1", "--client-event-id", "evt-stage-001", "--occurred-at", "2026-09-28T10:00:00Z"); err != nil {
		t.Fatalf("stage event: %v", err)
	}
	b := api.calls()[0].Body
	want := map[string]any{"type": "stage", "status": "pr_open", "prUrl": "https://github.com/acme/app/pull/9", "prNumber": float64(9), "branch": "wo-abc", "headSha": head, "autopilotStage": "PR_OPEN", "autopilotRunId": "run-1", "clientEventId": "evt-stage-001", "occurredAt": "2026-09-28T10:00:00Z", "clientName": "zensu-cli", "clientVersion": "dev"}
	for k, v := range want {
		if b[k] != v {
			t.Errorf("stage body[%s] = %v, want %v", k, b[k], v)
		}
	}
	if _, ok := b["detail"]; ok {
		t.Error("stage events carry no detail")
	}
	if out.String() != "Recorded stage event evt-stage-001; work order "+woID+" is pr_open\n" {
		t.Fatalf("output = %q", out.String())
	}

	f2, _ := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "event", woID, "--artifact", "plan", "--url", "https://github.com/acme/app/blob/x/plan.md", "--path", ".zensu/plans/x.md", "--line", "3", "--sha", "abcdef1", "--summary", "Implementation plan"); err != nil {
		t.Fatalf("artifact event: %v", err)
	}
	a := api.calls()[1].Body
	if a["type"] != "artifact" || !generatedEventID.MatchString(a["clientEventId"].(string)) {
		t.Fatalf("artifact body = %v", a)
	}
	d := a["detail"].(map[string]any)
	if d["kind"] != "plan" || d["url"] != "https://github.com/acme/app/blob/x/plan.md" || d["path"] != ".zensu/plans/x.md" || d["line"] != float64(3) || d["sha"] != "abcdef1" || d["summary"] != "Implementation plan" {
		t.Fatalf("artifact detail = %v", d)
	}
	if _, ok := a["status"]; ok {
		t.Error("artifact events carry no status")
	}

	f3, _ := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f3), "event", woID, "--blocked", "plan_approval", "--reason", "The plan waits for approval."); err != nil {
		t.Fatalf("blocked event: %v", err)
	}
	bl := api.calls()[2].Body
	if bl["type"] != "blocked" || bl["blockedKind"] != "plan_approval" || bl["detail"].(map[string]any)["reason"] != "The plan waits for approval." {
		t.Fatalf("blocked body = %v", bl)
	}
}

func TestWorkEvent_BlockedAcceptsTheReasonFromDetail(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/events": reply(201, map[string]any{"event": map[string]any{"id": "e"}, "order": orderJSON("blocked"), "replay": false}),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "event", woID, "--blocked", "worker_error", "--detail", `{"reason":"The toolchain is missing."}`, "--client-event-id", "evt-block-001"); err != nil {
		t.Fatalf("blocked event: %v", err)
	}
	d := api.only(t, "POST", "/api/work-orders/"+woID+"/events").Body["detail"].(map[string]any)
	if len(d) != 1 || d["reason"] != "The toolchain is missing." {
		t.Fatalf("detail = %v", d)
	}
	if out.String() != "Recorded blocked event evt-block-001; work order "+woID+" is blocked\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkEvent_RawDetailReplayAndJSON(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/events": reply(200, map[string]any{"event": map[string]any{"id": "e"}, "order": orderJSON("implementing"), "replay": true}),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "event", woID, "--artifact", "report", "--detail", `{"url":"https://example.com/r","summary":"s"}`, "--client-event-id", "evt-replay-01"); err != nil {
		t.Fatalf("event: %v", err)
	}
	d := api.calls()[0].Body["detail"].(map[string]any)
	if d["kind"] != "report" || d["url"] != "https://example.com/r" || d["summary"] != "s" {
		t.Fatalf("detail = %v", d)
	}
	if !strings.HasSuffix(out.String(), " (replayed)\n") {
		t.Fatalf("output = %q", out.String())
	}
	f2, out2 := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "event", woID, "--stage", "implementing", "--json"); err != nil {
		t.Fatalf("event json: %v", err)
	}
	if !strings.Contains(out2.String(), `"replay": true`) {
		t.Fatalf("json output = %q", out2.String())
	}
}

func TestWorkEvent_Validation(t *testing.T) {
	_, parseErr := time.Parse(time.RFC3339, "now")
	srv, api := newFakeAPI(t, nil)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"event", woID}, "exactly one of --stage, --artifact or --blocked is required"},
		{[]string{"event", woID, "--stage", "pr_open", "--blocked", "worker_error"}, "exactly one of --stage, --artifact or --blocked is required"},
		{[]string{"event", woID, "--artifact", "plan", "--detail", "[1]"}, "--detail must be a JSON object"},
		{[]string{"event", woID, "--stage", "pr_open", "--occurred-at", "now"}, "--occurred-at must be an RFC 3339 timestamp: " + parseErr.Error()},
		{[]string{"event", woID, "--stage", "pr_open", "--detail", `{"note":"x"}`}, "--detail does not apply to --stage events; stage events carry only their flags"},
		{[]string{"event", woID, "--blocked", "worker_error"}, "--blocked needs --reason"},
		{[]string{"event", woID, "--blocked", "worker_error", "--reason", "  "}, "--blocked needs --reason"},
		{[]string{"event", woID, "--blocked", "worker_error", "--detail", `{"reason":7}`}, "--blocked needs --reason"},
	} {
		f, _ := workSessionFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), tc.args...); err == nil || err.Error() != tc.want {
			t.Errorf("%v: error = %v, want %q", tc.args, err, tc.want)
		}
	}
	if len(api.calls()) != 0 {
		t.Fatal("invalid events must not reach the server")
	}
}

func TestWorkAsk(t *testing.T) {
	blocked := orderJSON("blocked")
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/questions": reply(201, map[string]any{"question": map[string]any{"id": woQ, "status": "awaiting_human"}, "order": blocked, "escalated": true, "replay": false}),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "ask", woID, "--category", "scope", "--question", "Should refunds cover partial captures?", "--option", "yes", "--option", "no", "--default-option", "no", "--blocking", "--requirement", "AC-001,FR-002", "--client-event-id", "ask-00000001"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	b := api.calls()[0].Body
	if b["category"] != "scope" || b["question"] != "Should refunds cover partial captures?" || b["blocking"] != true || b["defaultOption"] != "no" || b["clientEventId"] != "ask-00000001" {
		t.Fatalf("ask body = %v", b)
	}
	if opts := b["options"].([]any); len(opts) != 2 || opts[1] != "no" {
		t.Fatalf("options = %v", b["options"])
	}
	if reqs := b["requirementIds"].([]any); len(reqs) != 2 || reqs[0] != "AC-001" {
		t.Fatalf("requirementIds = %v", b["requirementIds"])
	}
	if out.String() != "Asked question "+woQ+" (awaiting_human); work order "+woID+" is blocked; escalated to a human; stop the session\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkAsk_BlockingNeedsNoDefault(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/questions": reply(201, map[string]any{"question": map[string]any{"id": woQ, "status": "awaiting_human"}, "order": orderJSON("blocked"), "escalated": true, "replay": false}),
	})
	f, _ := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "ask", woID, "--category", "product_decision", "--question", "Which plan tier gets refunds?", "--blocking"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	b := api.only(t, "POST", "/api/work-orders/"+woID+"/questions").Body
	if _, ok := b["defaultOption"]; ok || b["blocking"] != true {
		t.Fatalf("body = %v, want a blocking question without a default", b)
	}
}

func TestWorkAsk_NonBlockingJSONAndValidation(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/questions": reply(201, `{"question":{"id":"q"},"order":{"id":"o","status":"implementing"},"escalated":false,"replay":false}`),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "ask", woID, "--category", "clarification", "--question", "Which locale?", "--default-option", "en-US", "--json"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	b := api.calls()[0].Body
	for _, k := range []string{"options", "requirementIds"} {
		if _, ok := b[k]; ok {
			t.Errorf("%s must be omitted when not given", k)
		}
	}
	if b["blocking"] != false || b["defaultOption"] != "en-US" || !generatedEventID.MatchString(b["clientEventId"].(string)) {
		t.Fatalf("body = %v", b)
	}
	if !strings.Contains(out.String(), `"escalated": false`) {
		t.Fatalf("output = %q", out.String())
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"ask", woID, "--category", "scope"}, "--category and --question are required"},
		{[]string{"ask", woID, "--category", "scope", "--question", "Which locale?"}, "--default-option is required unless --blocking is set"},
	} {
		f, _ := workSessionFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), tc.args...); err == nil || err.Error() != tc.want {
			t.Errorf("%v: error = %v, want %q", tc.args, err, tc.want)
		}
	}
	if n := len(api.calls()); n != 1 {
		t.Fatalf("questions sent = %d, want only the valid one", n)
	}
}

func TestWorkAsk_TextOutputForAnOpenQuestion(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/questions": reply(201, `{"question":{"id":"q1","status":"open"},"order":{"id":"o1","status":"implementing"},"escalated":false}`),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "ask", woID, "--category", "clarification", "--question", "Which locale?", "--default-option", "en-US"); err != nil {
		t.Fatalf("ask: %v", err)
	}
	if out.String() != "Asked question q1 (open); work order o1 is implementing\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkFollowup(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/followups": reply(201, `{"event":{"id":"e"},"order":{"id":"o"},"replay":false}`),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "followup", woID, "--title", "Retry the refund webhook", "--rationale", "Webhooks can arrive twice.", "--severity", "medium", "--path", "backend/refunds.go", "--path", "docs/refunds,partial.md", "--client-event-id", "fu-00000001"); err != nil {
		t.Fatalf("followup: %v", err)
	}
	b := api.calls()[0].Body
	if b["title"] != "Retry the refund webhook" || b["rationale"] != "Webhooks can arrive twice." || b["severity"] != "medium" || b["clientEventId"] != "fu-00000001" {
		t.Fatalf("body = %v", b)
	}
	if paths := b["paths"].([]any); len(paths) != 2 || paths[0] != "backend/refunds.go" || paths[1] != "docs/refunds,partial.md" {
		t.Fatalf("paths = %v, want a path with a comma kept whole", b["paths"])
	}
	if out.String() != "Reported follow-up \"Retry the refund webhook\" for work order "+woID+" (event fu-00000001)\n" {
		t.Fatalf("output = %q", out.String())
	}
	f2, out2 := workSessionFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "followup", woID, "--title", "t", "--rationale", "r", "--severity", "low", "--json"); err != nil {
		t.Fatalf("followup json: %v", err)
	}
	if b2 := api.calls()[1].Body; b2["rationale"] != "r" || b2["severity"] != "low" || b2["paths"] != nil {
		t.Fatalf("paths must be omitted when none are given: %v", b2)
	}
	if !strings.Contains(out2.String(), `"replay": false`) {
		t.Fatalf("json output = %q", out2.String())
	}
}

func TestWorkFollowup_Validation(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	tooMany := []string{"followup", woID, "--title", "t", "--rationale", "r", "--severity", "low"}
	for i := 0; i <= maxFollowupPaths; i++ {
		tooMany = append(tooMany, "--path", fmt.Sprintf("f%d.go", i))
	}
	cases := map[string][]string{
		"--title and --rationale are required":                                     {"followup", woID, "--rationale", "r", "--severity", "low"},
		"--title and --rationale are required ":                                    {"followup", woID, "--title", "t", "--severity", "low"},
		"--severity must be one of low, medium, high, critical":                    {"followup", woID, "--title", "t", "--rationale", "r"},
		"--severity must be one of low, medium, high, critical ":                   {"followup", woID, "--title", "t", "--rationale", "r", "--severity", "urgent"},
		"at most 20 --path values":                                                 tooMany,
		`--path "backend/**" must be a clean relative file path without wildcards`: {"followup", woID, "--title", "t", "--rationale", "r", "--severity", "low", "--path", "backend/**"},
	}
	for want, args := range cases {
		t.Run(want, func(t *testing.T) {
			f, _ := workSessionFactory(srv)
			if err := runCmd(t, NewWorkCmd(f), args...); err == nil || err.Error() != strings.TrimSpace(want) {
				t.Fatalf("error = %v, want %q", err, strings.TrimSpace(want))
			}
		})
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("invalid follow-ups must not reach the API: %v", api.summary())
	}
}

func TestWorkAnswerAndOverturn(t *testing.T) {
	answered := map[string]any{"question": map[string]any{"id": woQ, "status": "answered"}, "order": orderJSON("queued"), "requeued": true}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-questions/" + woQ + "/answer":   reply(200, answered),
		"POST /api/work-questions/" + woQ + "/overturn": reply(200, map[string]any{"question": map[string]any{"id": woQ, "status": "answered"}, "order": nil, "requeued": false}),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "answer", woQ, "--answer", "Yes, cover partial captures.", "--rationale", "Finance asked for it.", "--plan-wide"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	b := api.only(t, "POST", "/api/work-questions/"+woQ+"/answer").Body
	if b["answer"] != "Yes, cover partial captures." || b["rationale"] != "Finance asked for it." || b["planWide"] != true {
		t.Fatalf("answer body = %v", b)
	}
	if out.String() != "Question "+woQ+" is answered; work order "+woID+" is queued (requeued)\n" {
		t.Fatalf("answer output = %q", out.String())
	}
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "overturn", woQ, "--answer", "No."); err != nil {
		t.Fatalf("overturn: %v", err)
	}
	if raw := api.only(t, "POST", "/api/work-questions/"+woQ+"/overturn").Raw; raw != `{"answer":"No."}` {
		t.Fatalf("overturn body = %s, want only the answer", raw)
	}
	if out2.String() != "Question "+woQ+" is answered\n" {
		t.Fatalf("overturn output = %q", out2.String())
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f3), "answer", woQ, "--answer", "x", "--json"); err != nil {
		t.Fatalf("answer json: %v", err)
	}
	if !strings.Contains(out3.String(), `"requeued": true`) {
		t.Fatalf("json output = %q", out3.String())
	}
	f4, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f4), "answer", woQ); err == nil || err.Error() != "--answer is required" {
		t.Fatalf("error = %v", err)
	}
	f5, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f5), "overturn", woQ, "--answer", "No.", "--plan-wide"); err == nil || err.Error() != "unknown flag: --plan-wide" {
		t.Fatalf("overturn --plan-wide: error = %v", err)
	}
	if n := api.count("POST", "/api/work-questions/"+woQ+"/overturn"); n != 1 {
		t.Fatalf("overturn requests = %d, want the refused call to send nothing", n)
	}
}

func TestWorkQuestions(t *testing.T) {
	list := map[string]any{"data": []any{
		map[string]any{"id": "q1", "work_order_id": woID, "category": "scope", "question": "Cover partial captures?", "blocking": true, "status": "awaiting_human"},
		map[string]any{"id": "q2", "work_order_id": "other", "category": "clarification", "question": "Locale?", "blocking": false, "status": "answered"},
	}}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + woPlan + "/questions": reply(200, list),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "questions", "--plan", woPlan, "--status", "awaiting_human", "--scope", "package"); err != nil {
		t.Fatalf("questions: %v", err)
	}
	q := api.calls()[0].Query
	if q.Get("status") != "awaiting_human" || q.Get("scope") != "package" {
		t.Fatalf("query = %v", q)
	}
	for _, want := range []string{"q1", "Cover partial captures?", "q2", "true"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("table misses %q:\n%s", want, out.String())
		}
	}
	assertWorkColumnsAligned(t, out.String(), map[string]string{"BLOCKING": "true", "ORDER": woID, "QUESTION": "Cover partial captures?"})
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "questions", "--plan", woPlan, "--order", woID); err != nil {
		t.Fatalf("questions --order: %v", err)
	}
	if strings.Contains(out2.String(), "q2") || !strings.Contains(out2.String(), "q1") {
		t.Fatalf("--order must keep only the order's questions:\n%s", out2.String())
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f3), "questions", "--plan", woPlan, "--order", woID, "--json"); err != nil {
		t.Fatalf("questions json: %v", err)
	}
	if strings.Contains(out3.String(), `"q2"`) || !strings.Contains(out3.String(), `"q1"`) {
		t.Fatalf("filtered json = %s", out3.String())
	}
	f4, out4 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f4), "questions", "--plan", woPlan, "--json"); err != nil {
		t.Fatalf("questions json: %v", err)
	}
	if !strings.Contains(out4.String(), `"q2"`) {
		t.Fatalf("unfiltered json = %s", out4.String())
	}
	f5, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f5), "questions"); err == nil || err.Error() != "--plan is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestWorkPolicy(t *testing.T) {
	current := map[string]any{
		"dispatch_mode": "manual", "plan_approval": "required", "max_running_orders": 2, "max_attempts": 3,
		"max_unconfirmed_claims": 5, "lease_minutes": 30, "max_order_runtime_minutes": 240, "max_order_cost_usd": nil,
		"daily_order_cap": 50, "allowed_api_key_ids": []any{"k1"}, "max_questions_per_order": 3, "question_reminder_hours": 24,
	}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, current),
		"PUT /api/products/" + woProduct + "/automation-policy": echoAllowedKeys,
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "policy", "get", "--product", woProduct); err != nil {
		t.Fatalf("policy get: %v", err)
	}
	if !strings.Contains(out.String(), `"plan_approval": "required"`) {
		t.Fatalf("get output = %q", out.String())
	}
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "policy", "set", "--product", woProduct, "--plan-approval", "auto", "--max-running-orders", "4", "--max-order-cost-usd", "12.5", "--allowed-key", woAgentKey, "--allowed-key", woAgentKey2); err != nil {
		t.Fatalf("policy set: %v", err)
	}
	put := api.only(t, "PUT", "/api/products/"+woProduct+"/automation-policy").Body
	want := map[string]any{"dispatchMode": "manual", "planApproval": "auto", "maxRunningOrders": float64(4), "maxAttempts": float64(3), "maxUnconfirmedClaims": float64(5), "leaseMinutes": float64(30), "maxOrderRuntimeMinutes": float64(240), "maxOrderCostUsd": 12.5, "dailyOrderCap": float64(50), "maxQuestionsPerOrder": float64(3), "questionReminderHours": float64(24)}
	for k, v := range want {
		if put[k] != v {
			t.Errorf("put[%s] = %v, want %v", k, put[k], v)
		}
	}
	if keys := put["allowedApiKeyIds"].([]any); len(keys) != 2 || keys[0] != woAgentKey || keys[1] != woAgentKey2 {
		t.Fatalf("allowedApiKeyIds = %v", put["allowedApiKeyIds"])
	}
	if out2.String() != "Updated the automation policy of product "+woProduct+"\nAllowed agent keys: "+woAgentKey+", "+woAgentKey2+"\n" {
		t.Fatalf("set output = %q", out2.String())
	}
}

func echoAllowedKeys(r recordedRequest) fakeReply {
	return fakeReply{Body: map[string]any{"product_id": woProduct, "allowed_api_key_ids": r.Body["allowedApiKeyIds"]}}
}

func TestWorkPolicySet_AddsAndRemovesAllowedKeysOfTheListItRead(t *testing.T) {
	const woAgentKey3 = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	cases := []struct {
		name    string
		current []any
		args    []string
		want    []string
		printed string
	}{
		{"the hint keeps the key already allowed", []any{woAgentKey}, []string{"--add-allowed-key", woAgentKey2}, []string{woAgentKey, woAgentKey2}, woAgentKey + ", " + woAgentKey2},
		{"a key already allowed in another case is not added twice", []any{woAgentKey}, []string{"--add-allowed-key", strings.ToUpper(woAgentKey)}, []string{woAgentKey}, woAgentKey},
		{"remove drops only the named key", []any{woAgentKey, woAgentKey2, woAgentKey3}, []string{"--remove-allowed-key", strings.ToUpper(woAgentKey2)}, []string{woAgentKey, woAgentKey3}, woAgentKey + ", " + woAgentKey3},
		{"add and remove combine", []any{woAgentKey, woAgentKey2}, []string{"--remove-allowed-key", woAgentKey, "--add-allowed-key", woAgentKey3}, []string{woAgentKey2, woAgentKey3}, woAgentKey2 + ", " + woAgentKey3},
		{"removing the last key", []any{woAgentKey}, []string{"--remove-allowed-key", woAgentKey}, []string{}, "none"},
		{"adding to a policy without keys", nil, []string{"--add-allowed-key", woAgentKey}, []string{woAgentKey}, woAgentKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := map[string]any{"dispatch_mode": "manual", "plan_approval": "required", "max_running_orders": 2, "max_attempts": 3, "lease_minutes": 30, "allowed_api_key_ids": tc.current}
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"GET /api/products/" + woProduct + "/automation-policy": reply(200, current),
				"PUT /api/products/" + woProduct + "/automation-policy": echoAllowedKeys,
			})
			f, out := testFactory(srv)
			if err := runCmd(t, NewWorkCmd(f), append([]string{"policy", "set", "--product", woProduct}, tc.args...)...); err != nil {
				t.Fatalf("policy set: %v", err)
			}
			put := api.only(t, "PUT", "/api/products/"+woProduct+"/automation-policy").Body
			keys, ok := put["allowedApiKeyIds"].([]any)
			if !ok || len(keys) != len(tc.want) {
				t.Fatalf("allowedApiKeyIds = %v, want %v", put["allowedApiKeyIds"], tc.want)
			}
			for i, key := range tc.want {
				if keys[i] != key {
					t.Fatalf("allowedApiKeyIds = %v, want %v", keys, tc.want)
				}
			}
			if put["planApproval"] != "required" || put["maxRunningOrders"] != float64(2) || put["maxAttempts"] != float64(3) || put["leaseMinutes"] != float64(30) {
				t.Fatalf("the other values must be kept: %v", put)
			}
			if out.String() != "Updated the automation policy of product "+woProduct+"\nAllowed agent keys: "+tc.printed+"\n" {
				t.Fatalf("output = %q", out.String())
			}
		})
	}
}

func TestWorkPolicySet_RefusesConflictingKeyFlagsBeforeAnyRequest(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--allowed-key", woAgentKey, "--add-allowed-key", woAgentKey2}, "if any flags in the group [allowed-key add-allowed-key] are set none of the others can be; [add-allowed-key allowed-key] were all set"},
		{[]string{"--allowed-key", woAgentKey, "--remove-allowed-key", woAgentKey2}, "if any flags in the group [allowed-key remove-allowed-key] are set none of the others can be; [allowed-key remove-allowed-key] were all set"},
		{[]string{"--add-allowed-key", woAgentKey, "--remove-allowed-key", strings.ToUpper(woAgentKey)}, "--add-allowed-key and --remove-allowed-key both name " + woAgentKey + "; name each key once"},
	} {
		f, out := testFactory(srv)
		err := runCmd(t, NewWorkCmd(f), append([]string{"policy", "set", "--product", woProduct}, tc.args...)...)
		if err == nil || err.Error() != tc.want || out.Len() != 0 {
			t.Errorf("%v: error = %v, output %q, want %q", tc.args, err, out.String(), tc.want)
		}
	}
	if n := len(api.calls()); n != 0 {
		t.Fatalf("refused flag combinations sent %d requests: %v", n, api.summary())
	}
}

func TestWorkPolicySet_NamesTheFlagThatSuppliedRefusedKeys(t *testing.T) {
	message := "allowedApiKeyIds must name active keys of this organization whose only scope is agent; not usable: " + woAgentKey2
	refused := `{"code":"invalid_agent_keys","message":"` + message + `"}`
	for _, tc := range []struct {
		name  string
		args  []string
		reply string
		want  string
	}{
		{"add", []string{"--add-allowed-key", woAgentKey2}, refused, message + " (status 422); the refused keys came from --add-allowed-key"},
		{"replace", []string{"--allowed-key", woAgentKey2}, refused, message + " (status 422); the refused keys came from --allowed-key"},
		{"no key flag", []string{"--max-attempts", "4"}, refused, message + " (status 422)"},
		{"another refusal", []string{"--add-allowed-key", woAgentKey2}, `{"code":"invalid_policy","message":"at most 100 allowed agent keys"}`, "at most 100 allowed agent keys (status 422)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"GET /api/products/" + woProduct + "/automation-policy": reply(200, map[string]any{"dispatch_mode": "manual", "allowed_api_key_ids": []any{woAgentKey}}),
				"PUT /api/products/" + woProduct + "/automation-policy": reply(422, tc.reply),
			})
			f, out := testFactory(srv)
			err := runCmd(t, NewWorkCmd(f), append([]string{"policy", "set", "--product", woProduct}, tc.args...)...)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v\nwant    %s", err, tc.want)
			}
			if api.count("PUT", "/api/products/"+woProduct+"/automation-policy") != 1 || out.Len() != 0 {
				t.Fatalf("calls %v, output %q", api.summary(), out.String())
			}
		})
	}
}

func TestWorkPolicySet_KeepsUnsetValuesAndPrintsJSON(t *testing.T) {
	current := `{"dispatch_mode":"manual","plan_approval":"auto","max_running_orders":1,"max_attempts":2,"max_unconfirmed_claims":3,"lease_minutes":20,"max_order_runtime_minutes":60,"max_order_cost_usd":7,"daily_order_cap":9,"allowed_api_key_ids":null,"max_questions_per_order":4,"question_reminder_hours":12}`
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, current),
		"PUT /api/products/" + woProduct + "/automation-policy": reply(200, current),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "policy", "set", "--product", woProduct, "--lease-minutes", "45", "--daily-order-cap", "10", "--max-attempts", "5", "--max-unconfirmed-claims", "6", "--max-order-runtime-minutes", "90", "--max-questions-per-order", "7", "--question-reminder-hours", "48", "--dispatch-mode", "manual", "--json"); err != nil {
		t.Fatalf("policy set: %v", err)
	}
	put := api.only(t, "PUT", "/api/products/"+woProduct+"/automation-policy").Body
	if put["leaseMinutes"] != float64(45) || put["dailyOrderCap"] != float64(10) || put["maxAttempts"] != float64(5) || put["maxUnconfirmedClaims"] != float64(6) || put["maxOrderRuntimeMinutes"] != float64(90) || put["maxQuestionsPerOrder"] != float64(7) || put["questionReminderHours"] != float64(48) {
		t.Fatalf("put = %v", put)
	}
	if put["maxRunningOrders"] != float64(1) || put["maxOrderCostUsd"] != float64(7) || put["planApproval"] != "auto" {
		t.Fatalf("unset values must be kept: %v", put)
	}
	if keys, ok := put["allowedApiKeyIds"].([]any); !ok || len(keys) != 0 {
		t.Fatalf("a null key list must be sent as an empty list: %v", put["allowedApiKeyIds"])
	}
	if !strings.Contains(out.String(), `"dispatch_mode": "manual"`) {
		t.Fatalf("json output = %q", out.String())
	}
}

func TestWorkPolicySet_ClearsTheOrderCostCap(t *testing.T) {
	current := `{"dispatch_mode":"manual","plan_approval":"auto","max_running_orders":1,"max_attempts":2,"max_unconfirmed_claims":3,"lease_minutes":20,"max_order_runtime_minutes":60,"max_order_cost_usd":7,"daily_order_cap":9,"allowed_api_key_ids":[],"max_questions_per_order":4,"question_reminder_hours":12}`
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, current),
		"PUT /api/products/" + woProduct + "/automation-policy": reply(200, current),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "policy", "set", "--product", woProduct, "--clear-max-order-cost"); err != nil {
		t.Fatalf("policy set: %v", err)
	}
	want := `{"allowedApiKeyIds":[],"dailyOrderCap":9,"dispatchMode":"manual","leaseMinutes":20,"maxAttempts":2,"maxOrderCostUsd":null,"maxOrderRuntimeMinutes":60,"maxQuestionsPerOrder":4,"maxRunningOrders":1,"maxUnconfirmedClaims":3,"planApproval":"auto","questionReminderHours":12}`
	if put := api.only(t, "PUT", "/api/products/"+woProduct+"/automation-policy").Raw; put != want {
		t.Fatalf("put body = %s, want %s", put, want)
	}
	if out.String() != "Updated the automation policy of product "+woProduct+"\nAllowed agent keys: none\n" {
		t.Fatalf("output = %q", out.String())
	}
	f2, _ := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f2), "policy", "set", "--product", woProduct, "--clear-max-order-cost", "--max-order-cost-usd", "5")
	if err == nil || !strings.Contains(err.Error(), "[clear-max-order-cost max-order-cost-usd] were all set") {
		t.Fatalf("error = %v, want the two cost flags refused together", err)
	}
	if n := api.count("GET", "/api/products/"+woProduct+"/automation-policy"); n != 1 {
		t.Fatalf("policy reads = %d, want none for the refused call", n)
	}
}

func TestWorkPolicy_RequiresProduct(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	for _, args := range [][]string{{"policy", "get"}, {"policy", "set"}} {
		f, _ := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), args...); err == nil || err.Error() != "--product is required" {
			t.Errorf("%v: error = %v", args, err)
		}
	}
	if len(api.calls()) != 0 {
		t.Fatal("no request without a product")
	}
}

func TestWorkRepositories(t *testing.T) {
	repoID := "55555555-6666-4777-8888-999999999999"
	echoRiskPaths := func(r recordedRequest) fakeReply {
		return fakeReply{Status: 200, Body: map[string]any{"id": repoID, "risk_paths": r.Body["riskPaths"]}}
	}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/repositories":              reply(200, map[string]any{"data": []any{map[string]any{"id": repoID, "repository": "https://github.com/acme/app", "risk_paths": []any{"a/**", "b/**"}}}}),
		"POST /api/products/" + woProduct + "/repositories":             reply(201, map[string]any{"id": repoID}),
		"PATCH /api/products/" + woProduct + "/repositories/" + repoID:  echoRiskPaths,
		"DELETE /api/products/" + woProduct + "/repositories/" + repoID: reply(204, nil),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "repositories", "list", "--product", woProduct); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "https://github.com/acme/app") || !strings.Contains(out.String(), "a/**, b/**") {
		t.Fatalf("list output = %q", out.String())
	}
	assertWorkColumnsAligned(t, out.String(), map[string]string{"REPOSITORY": "https://github.com/acme/app", "RISK PATHS": "a/**, b/**"})
	f2, out2 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f2), "repos", "add", "--product", woProduct, "--repository", "https://github.com/acme/app", "--risk-path", "backend/internal/auth/**", "--risk-path", "docs/{a,b}/**"); err != nil {
		t.Fatalf("add: %v", err)
	}
	add := api.only(t, "POST", "/api/products/"+woProduct+"/repositories").Body
	if paths := add["riskPaths"].([]any); add["repository"] != "https://github.com/acme/app" || len(paths) != 2 || paths[0] != "backend/internal/auth/**" || paths[1] != "docs/{a,b}/**" {
		t.Fatalf("add body = %v, want a glob with a comma kept whole", add)
	}
	if out2.String() != "Registered https://github.com/acme/app ("+repoID+")\n" {
		t.Fatalf("add output = %q", out2.String())
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f3), "repositories", "update", repoID, "--product", woProduct, "--risk-path", "backend/{auth,rls}/**", "--risk-path", "migrations/**"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if raw := api.only(t, "PATCH", "/api/products/"+woProduct+"/repositories/"+repoID).Raw; raw != `{"riskPaths":["backend/{auth,rls}/**","migrations/**"]}` {
		t.Fatalf("update body = %s", raw)
	}
	if out3.String() != "Updated the risk paths of repository "+repoID+": backend/{auth,rls}/**, migrations/**\n" {
		t.Fatalf("update output = %q", out3.String())
	}
	f4, out4 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f4), "repositories", "remove", repoID, "--product", woProduct); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if out4.String() != "Removed repository "+repoID+"\n" {
		t.Fatalf("remove output = %q", out4.String())
	}
}

func TestWorkRepositoriesUpdate_ClearsOnlyWhenAsked(t *testing.T) {
	repoID := "55555555-6666-4777-8888-999999999999"
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"PATCH /api/products/" + woProduct + "/repositories/" + repoID: reply(200, map[string]any{"id": repoID, "risk_paths": []any{}}),
	})
	f, out := testFactory(srv)
	err := runCmd(t, NewWorkCmd(f), "repositories", "update", repoID, "--product", woProduct)
	if err == nil || err.Error() != "--risk-path or --clear is required; the update replaces every risk path of the repository" {
		t.Fatalf("error = %v", err)
	}
	f2, _ := testFactory(srv)
	err = runCmd(t, NewWorkCmd(f2), "repositories", "update", repoID, "--product", woProduct, "--clear", "--risk-path", "a/**")
	if err == nil || !strings.Contains(err.Error(), "[clear risk-path] were all set") {
		t.Fatalf("error = %v, want --clear and --risk-path refused together", err)
	}
	if n := len(api.calls()); n != 0 || out.Len() != 0 {
		t.Fatalf("refused updates sent %d requests", n)
	}
	f3, out3 := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f3), "repositories", "update", repoID, "--product", woProduct, "--clear"); err != nil {
		t.Fatalf("update --clear: %v", err)
	}
	if raw := api.only(t, "PATCH", "/api/products/"+woProduct+"/repositories/"+repoID).Raw; raw != `{"riskPaths":[]}` {
		t.Fatalf("update body = %s, want an empty list", raw)
	}
	if out3.String() != "Updated the risk paths of repository "+repoID+": none\n" {
		t.Fatalf("output = %q", out3.String())
	}
}

func TestWorkRepositories_JSONAndValidation(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/repositories":      reply(200, `{"data":[]}`),
		"POST /api/products/" + woProduct + "/repositories":     reply(201, `{"id":"r1"}`),
		"PATCH /api/products/" + woProduct + "/repositories/r1": reply(200, `{"id":"r1"}`),
	})
	for _, args := range [][]string{
		{"repositories", "list", "--product", woProduct, "--json"},
		{"repositories", "add", "--product", woProduct, "--repository", "https://github.com/acme/x", "--json"},
		{"repositories", "update", "r1", "--product", woProduct, "--risk-path", "x/**", "--json"},
	} {
		f, out := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out.String(), "{") {
			t.Fatalf("%v output = %q", args, out.String())
		}
	}
	if add := api.only(t, "POST", "/api/products/"+woProduct+"/repositories").Body; len(add["riskPaths"].([]any)) != 0 {
		t.Fatalf("add without risk paths must send an empty list: %v", add)
	}
	for _, args := range [][]string{
		{"repositories", "list"},
		{"repositories", "add", "--product", woProduct},
		{"repositories", "update", "r1"},
		{"repositories", "remove", "r1"},
	} {
		f, _ := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), args...); err == nil {
			t.Errorf("%v must fail", args)
		}
	}
}

func TestWorkCommands_InvalidResponses(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-orders":                                  reply(200, `[]`),
		"GET /api/work-orders/" + woID + "/events":              reply(200, `[]`),
		"POST /api/work-orders/" + woID + "/approve":            reply(200, `[]`),
		"POST /api/work-orders/claim":                           reply(200, `[]`),
		"POST /api/work-orders/" + woID + "/heartbeat":          reply(200, `[]`),
		"POST /api/work-orders/" + woID + "/events":             reply(200, `[]`),
		"POST /api/work-orders/" + woID + "/questions":          reply(200, `[]`),
		"POST /api/work-questions/" + woQ + "/answer":           reply(200, `[]`),
		"GET /api/work-plans/" + woPlan + "/questions":          reply(200, `[]`),
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, `[]`),
		"GET /api/products/" + woProduct + "/repositories":      reply(200, `[]`),
		"POST /api/products/" + woProduct + "/work-orders":      reply(200, `[]`),
		"PATCH /api/products/" + woProduct + "/repositories/r1": reply(200, `[]`),
	})
	for _, tc := range []struct {
		args    []string
		session bool
	}{
		{[]string{"list"}, false},
		{[]string{"events", woID}, false},
		{[]string{"approve", woID}, false},
		{[]string{"claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app"}, false},
		{[]string{"heartbeat", woID}, true},
		{[]string{"event", woID, "--stage", "pr_open"}, true},
		{[]string{"ask", woID, "--category", "scope", "--question", "q", "--blocking"}, true},
		{[]string{"answer", woQ, "--answer", "a"}, false},
		{[]string{"questions", "--plan", woPlan}, false},
		{[]string{"policy", "set", "--product", woProduct}, false},
		{[]string{"repositories", "list", "--product", woProduct}, false},
		{[]string{"repositories", "update", "r1", "--product", woProduct, "--clear"}, false},
		{[]string{"create", "--product", woProduct, "--revision", woRevisionID, "--repository", "x"}, false},
	} {
		f, _ := testFactory(srv)
		if tc.session {
			f, _ = workSessionFactory(srv)
		}
		err := runCmd(t, NewWorkCmd(f), tc.args...)
		if err == nil || !strings.Contains(err.Error(), "invalid") {
			t.Errorf("%v: error = %v, want an invalid-response error", tc.args, err)
		}
	}
}

func TestLooksLikeFeatureKey(t *testing.T) {
	cases := map[string]bool{
		"ZEN-42": true, "AUTH-7": true, "A1-3": true, "ZEN-0": false, "ZEN-042": false, "zen-42": true,
		"ZEN-": false, "-42": false, "ZEN42": false, "1ZEN-4": false, "ZEN-4a": false,
		woFeature: false, "ZEN-99999999999999999999": false, "ZE N-1": false,
	}
	for in, want := range cases {
		if got := looksLikeFeatureKey(in); got != want {
			t.Errorf("looksLikeFeatureKey(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestWorkRequestsFailOnTransportErrors(t *testing.T) {
	f, _ := failingFactory()
	for _, args := range [][]string{
		{"get", woID}, {"list"}, {"approve", woID},
		{"claim", "--session-id", woSessionID, "--repository", "https://github.com/acme/app"},
		{"confirm", woID, "--session-id", woSessionID, "--attempt", "1"},
		{"release", woID, "--session-id", woSessionID, "--attempt", "1", "--outcome", "success"},
		{"policy", "get", "--product", woProduct}, {"repositories", "remove", "r", "--product", woProduct},
		{"repositories", "update", "r", "--product", woProduct, "--clear"},
		{"questions", "--plan", woPlan},
		{"create", "--product", woProduct, "--feature", "ZEN-1", "--repository", "r"},
		{"list", "--feature", "ZEN-1"},
	} {
		if err := runCmd(t, NewWorkCmd(f), args...); err == nil || !strings.Contains(err.Error(), "dial refused") {
			t.Errorf("%v: error = %v", args, err)
		}
	}
	session := workFailingSessionFactory()
	for _, args := range [][]string{
		{"heartbeat", woID},
		{"event", woID, "--stage", "pr_open"},
		{"ask", woID, "--category", "scope", "--question", "q", "--blocking"},
		{"followup", woID, "--title", "t", "--rationale", "r", "--severity", "low"},
	} {
		if err := runCmd(t, NewWorkCmd(session), args...); err == nil || !strings.Contains(err.Error(), "dial refused") {
			t.Errorf("%v: error = %v", args, err)
		}
	}
}

func TestWorkConfirmAndRelease_JSON(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/confirm": reply(200, `{"order":{"id":"o1","status":"claimed"},"lease_seconds":1800,"answers":[]}`),
		"POST /api/work-orders/" + woID + "/release": reply(200, `{"id":"o1","status":"queued"}`),
	})
	for _, args := range [][]string{
		{"confirm", woID, "--session-id", woSessionID, "--attempt", "1", "--json"},
		{"release", woID, "--session-id", woSessionID, "--attempt", "1", "--outcome", "interrupted", "--json"},
	} {
		f, out := testFactory(srv)
		if err := runCmd(t, NewWorkCmd(f), args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out.String(), "{\n") || !strings.Contains(out.String(), `"o1"`) {
			t.Fatalf("%v output = %q", args, out.String())
		}
	}
}

func TestWorkList_ResolveWithoutAnIDIsRefused(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/features/resolve": reply(200, `{}`),
	})
	f, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "list", "--feature", "auth-7"); err == nil || err.Error() != "resolve AUTH-7: unexpected response" {
		t.Fatalf("error = %v", err)
	}
	if api.count("GET", "/api/work-orders") != 0 || api.only(t, "GET", "/api/features/resolve").Query.Get("ref") != "AUTH-7" {
		t.Fatalf("calls = %v, want only the canonical resolve", api.summary())
	}
}
