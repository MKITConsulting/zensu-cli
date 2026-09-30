package cmd

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func policyVersionReply(version string, keys []any) map[string]any {
	return map[string]any{
		"dispatch_mode": "manual", "plan_approval": "required", "max_running_orders": 2, "max_attempts": 3,
		"lease_minutes": 30, "allowed_api_key_ids": keys, "updated_at": version,
	}
}

func TestWorkPolicySet_SendsTheVersionItRead(t *testing.T) {
	const version = "2026-09-30T12:00:00.123456Z"
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, policyVersionReply(version, []any{woAgentKey})),
		"PUT /api/products/" + woProduct + "/automation-policy": echoAllowedKeys,
	})
	f, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "policy", "set", "--product", woProduct, "--max-attempts", "4"); err != nil {
		t.Fatalf("policy set: %v", err)
	}
	put := api.only(t, "PUT", "/api/products/"+woProduct+"/automation-policy").Body
	if put["expectedUpdatedAt"] != version || put["maxAttempts"] != float64(4) {
		t.Fatalf("put = %v, want the version it read and the change", put)
	}
}

func TestWorkPolicySet_OmitsTheVersionWhenTheServerSendsNone(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, policyVersionReply("", []any{woAgentKey})),
		"PUT /api/products/" + woProduct + "/automation-policy": echoAllowedKeys,
	})
	f, _ := testFactory(srv)
	if err := runCmd(t, NewWorkCmd(f), "policy", "set", "--product", woProduct, "--max-attempts", "4"); err != nil {
		t.Fatalf("policy set: %v", err)
	}
	if _, sent := api.only(t, "PUT", "/api/products/"+woProduct+"/automation-policy").Body["expectedUpdatedAt"]; sent {
		t.Fatal("sent a version the server never named")
	}
}

func TestWorkPolicySet_RereadsWhenThePolicyChangedMeanwhile(t *testing.T) {
	const first, second = "2026-09-30T12:00:00.1Z", "2026-09-30T12:00:05.2Z"
	var mu sync.Mutex
	reads := 0
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": func(recordedRequest) fakeReply {
			mu.Lock()
			defer mu.Unlock()
			reads++
			if reads == 1 {
				return fakeReply{Status: 200, Body: policyVersionReply(first, []any{woAgentKey})}
			}
			return fakeReply{Status: 200, Body: policyVersionReply(second, []any{woAgentKey, woAgentKey2})}
		},
		"PUT /api/products/" + woProduct + "/automation-policy": func(r recordedRequest) fakeReply {
			if r.Body["expectedUpdatedAt"] == first {
				return fakeReply{Status: 409, Body: `{"code":"policy_changed","message":"the automation policy changed after it was read; read it again and repeat the change"}`}
			}
			return echoAllowedKeys(r)
		},
	})
	f, out := testFactory(srv)
	const added = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	work, notes := NewWorkCmd(f), &bytes.Buffer{}
	work.SetErr(notes)
	if err := runCmd(t, work, "policy", "set", "--product", woProduct, "--add-allowed-key", added); err != nil {
		t.Fatalf("policy set: %v", err)
	}
	if notes.String() != "note: the policy changed after it was read; reading it again (attempt 2 of 3)\n" {
		t.Fatalf("stderr = %q, want one note for the repeated attempt", notes.String())
	}
	var puts []recordedRequest
	for _, r := range api.calls() {
		if r.Method == "PUT" {
			puts = append(puts, r)
		}
	}
	if len(puts) != 2 || api.count("GET", "/api/products/"+woProduct+"/automation-policy") != 2 {
		t.Fatalf("calls = %v, want two reads and two writes", api.summary())
	}
	retry := puts[1].Body
	keys, _ := retry["allowedApiKeyIds"].([]any)
	if retry["expectedUpdatedAt"] != second || len(keys) != 3 || keys[0] != woAgentKey || keys[1] != woAgentKey2 || keys[2] != added {
		t.Fatalf("retry = %v, want the fresh version and the key added to the list read again", retry)
	}
	if !strings.Contains(out.String(), "Allowed agent keys: "+woAgentKey+", "+woAgentKey2+", "+added) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkPolicySet_GivesUpWhenThePolicyKeepsChanging(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, policyVersionReply("2026-09-30T12:00:00Z", []any{woAgentKey})),
		"PUT /api/products/" + woProduct + "/automation-policy": reply(409, `{"code":"policy_changed","message":"the automation policy changed after it was read; read it again and repeat the change"}`),
	})
	f, out := testFactory(srv)
	work, notes := NewWorkCmd(f), &bytes.Buffer{}
	work.SetErr(notes)
	err := runCmd(t, work, "policy", "set", "--product", woProduct, "--max-attempts", "4")
	want := "the automation policy changed after it was read; read it again and repeat the change (status 409); the policy changed 3 times while this command wrote it, run it again"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v\nwant    %s", err, want)
	}
	if api.count("PUT", "/api/products/"+woProduct+"/automation-policy") != policyWriteAttempts || api.count("GET", "/api/products/"+woProduct+"/automation-policy") != policyWriteAttempts || out.Len() != 0 {
		t.Fatalf("calls = %v output %q, want %d reads and writes and no output", api.summary(), out.String(), policyWriteAttempts)
	}
	if strings.Count(notes.String(), "reading it again") != policyWriteAttempts-1 || !strings.Contains(notes.String(), "(attempt 3 of 3)") {
		t.Fatalf("stderr = %q, want one note per repeated attempt", notes.String())
	}
}

func TestWorkPolicySet_WritesWithoutTheVersionForAServerThatDoesNotKnowIt(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/products/" + woProduct + "/automation-policy": reply(200, policyVersionReply("2026-09-30T12:00:00Z", []any{woAgentKey})),
		"PUT /api/products/" + woProduct + "/automation-policy": func(r recordedRequest) fakeReply {
			if _, sent := r.Body["expectedUpdatedAt"]; sent {
				return fakeReply{Status: 400, Body: `{"code":"invalid_body","message":"unknown field \"expectedUpdatedAt\""}`}
			}
			return echoAllowedKeys(r)
		},
	})
	f, out := testFactory(srv)
	work, notes := NewWorkCmd(f), &bytes.Buffer{}
	work.SetErr(notes)
	if err := runCmd(t, work, "policy", "set", "--product", woProduct, "--max-attempts", "4"); err != nil {
		t.Fatalf("policy set against a server without the version check: %v", err)
	}
	var puts []recordedRequest
	for _, r := range api.calls() {
		if r.Method == "PUT" {
			puts = append(puts, r)
		}
	}
	if len(puts) != 2 || api.count("GET", "/api/products/"+woProduct+"/automation-policy") != 2 {
		t.Fatalf("calls = %v, want a refused write and one repeat after a fresh read", api.summary())
	}
	if _, sent := puts[1].Body["expectedUpdatedAt"]; sent || puts[0].Body["expectedUpdatedAt"] != "2026-09-30T12:00:00Z" || puts[1].Body["maxAttempts"] != float64(4) {
		t.Fatalf("writes = %v then %v, want the version first and the same change without it", puts[0].Body, puts[1].Body)
	}
	if notes.String() != "note: this server does not check the policy version yet; writing without it, so a change someone else makes at the same moment is not detected\n" {
		t.Fatalf("stderr = %q, want the note that the write is not versioned", notes.String())
	}
	if !strings.Contains(out.String(), "Updated the automation policy of product "+woProduct) {
		t.Fatalf("output = %q", out.String())
	}
}

func TestWorkPolicySet_KeepsEveryOtherBodyRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, code, message string
		status              int
	}{
		{"another unknown field", "invalid_body", `unknown field \"leaseHours\"`, 400},
		{"another code that names the version", "invalid_policy", `expectedUpdatedAt must be an RFC 3339 timestamp`, 400},
		{"the same code refusing the value of the version", "invalid_body", `expectedUpdatedAt must be an RFC 3339 timestamp`, 400},
		{"the same code naming the version after another field", "invalid_body", `unknown field \"leaseHours\"; expectedUpdatedAt was read`, 400},
		{"a longer field that starts like the version", "invalid_body", `unknown field \"expectedUpdatedAtMillis\"`, 400},
		{"the same words under another status", "invalid_body", `unknown field \"expectedUpdatedAt\"`, 422},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"GET /api/products/" + woProduct + "/automation-policy": reply(200, policyVersionReply("2026-09-30T12:00:00Z", []any{woAgentKey})),
				"PUT /api/products/" + woProduct + "/automation-policy": reply(tc.status, `{"code":"`+tc.code+`","message":"`+tc.message+`"}`),
			})
			f, _ := testFactory(srv)
			work, notes := NewWorkCmd(f), &bytes.Buffer{}
			work.SetErr(notes)
			err := runCmd(t, work, "policy", "set", "--product", woProduct, "--max-attempts", "4")
			if err == nil || !strings.Contains(err.Error(), "(status "+strconv.Itoa(tc.status)+")") {
				t.Fatalf("error = %v, want the server's refusal", err)
			}
			if api.count("PUT", "/api/products/"+woProduct+"/automation-policy") != 1 || strings.Contains(notes.String(), "note:") {
				t.Fatalf("calls = %v stderr %q, want one write and no fallback", api.summary(), notes.String())
			}
		})
	}
}
