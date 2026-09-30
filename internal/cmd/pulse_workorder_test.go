package cmd

import (
	"strings"
	"testing"
)

func TestPulseStart_WorkOrderLinksTheSession(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/pulse-session": reply(201, map[string]any{"id": pulseTestSessionID, "work_order_id": woID, "attempt": 2, "created": true}),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewPulseCmd(f), "start", "--work-order", woID, "--head-sha", "abcdef1", "--branch", "wo-1", "--client-name", "claude-code", "--client-version", "2.1.0"); err != nil {
		t.Fatalf("pulse start --work-order: %v", err)
	}
	got := api.only(t, "POST", "/api/work-orders/"+woID+"/pulse-session")
	b := got.Body
	if len(b) != 4 || b["startSha"] != "abcdef1" || b["branch"] != "wo-1" || b["clientName"] != "claude-code" || b["clientVersion"] != "2.1.0" {
		t.Fatalf("body = %v", b)
	}
	if got.Auth != "Bearer "+woSessionToken || got.APIKey != "" {
		t.Fatalf("auth = %q key = %q, want the session token", got.Auth, got.APIKey)
	}
	if out.String() != "Linked session "+pulseTestSessionID+" to work order "+woID+"\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPulseStart_WorkOrderDefaultsAndOptionalHead(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/pulse-session": reply(200, map[string]any{"id": strings.ToUpper(pulseTestSessionID), "created": false}),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewPulseCmd(f), "start", "--work-order", woID, "--minimal-json"); err != nil {
		t.Fatalf("pulse start: %v", err)
	}
	b := api.only(t, "POST", "/api/work-orders/"+woID+"/pulse-session").Body
	if len(b) != 2 || b["clientName"] != "zensu-cli" || b["clientVersion"] != "dev" {
		t.Fatalf("body = %v", b)
	}
	if out.String() != "{\n  \"id\": \""+pulseTestSessionID+"\"\n}\n" {
		t.Fatalf("minimal json = %q", out.String())
	}
}

func TestPulseStart_WorkOrderTrackingDisabled(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/pulse-session": reply(200, `{"status":"tracking_disabled"}`),
	})
	f, out := workSessionFactory(srv)
	if err := runCmd(t, NewPulseCmd(f), "start", "--work-order", woID); err != nil {
		t.Fatalf("pulse start: %v", err)
	}
	if out.String() != "No Pulse session was created: the agent key's creator turned tracking off or is no longer an active member.\n" {
		t.Fatalf("output = %q", out.String())
	}
}

func TestPulseStart_WorkOrderRejectsProductAndProject(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	for _, extra := range [][]string{{"--product", "p"}, {"--project", "/repo"}} {
		f, _ := workSessionFactory(srv)
		args := append([]string{"start", "--work-order", woID}, extra...)
		if err := runCmd(t, NewPulseCmd(f), args...); err == nil || !strings.Contains(err.Error(), "do not apply to --work-order") {
			t.Fatalf("%v: error = %v", extra, err)
		}
	}
	if len(api.calls()) != 0 {
		t.Fatal("no request for a refused combination")
	}
}

func TestPulseStart_WorkOrderNeedsTheSessionToken(t *testing.T) {
	srv, api := newFakeAPI(t, nil)
	for _, extra := range [][]string{nil, {"--minimal-json"}} {
		f, out := testFactory(srv)
		args := append([]string{"start", "--work-order", woID}, extra...)
		err := runCmd(t, NewPulseCmd(f), args...)
		if err == nil || err.Error() != "zensu pulse start --work-order runs inside a work order session; set ZENSU_SESSION_TOKEN to the session token of its claim" || out.Len() != 0 {
			t.Fatalf("%v: error = %v output = %q", extra, err, out.String())
		}
	}
	if len(api.calls()) != 0 {
		t.Fatalf("a login or API key must not link a session: %v", api.summary())
	}
}

func TestPulseStart_WorkOrderSurfacesStaleAttempt(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"POST /api/work-orders/" + woID + "/pulse-session": reply(409, `{"code":"stale_attempt","message":"the order is held by another attempt or its lease expired; stop without further forge writes"}`),
	})
	f, _ := workSessionFactory(srv)
	err := runCmd(t, NewPulseCmd(f), "start", "--work-order", woID)
	if err == nil || !strings.Contains(err.Error(), "(status 409)") {
		t.Fatalf("error = %v", err)
	}
}
