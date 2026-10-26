package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnowledgeContextReceipts(t *testing.T) {
	const load = "11111111-1111-4111-8111-111111111111"
	const item = "22222222-2222-4222-8222-222222222222"
	for _, agent := range []string{"codex", "claude-code", "copilot", "kiro", "antigravity", "gemini-cli", "cli"} {
		t.Run(agent, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPost {
					t.Errorf("method %s", r.Method)
				}
				if calls == 1 {
					if r.URL.Path != "/api/knowledge/features/ZEN-42/context-loads" || r.URL.RawQuery != "depth=3&include=build%2Cpassages" {
						t.Errorf("pack URL %s", r.URL)
					}
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 1 || body["agent"] != agent {
						t.Errorf("agent payload %v %v", body, err)
					}
					w.WriteHeader(http.StatusCreated)
					fmt.Fprintf(w, `{"load_id":%q,"loaded_at":"2026-10-26T10:00:00Z","layers":{"build":[]}}`, load)
				} else {
					if r.URL.Path != "/api/knowledge/context-loads/"+load+"/items/"+item {
						t.Errorf("source URL %s", r.URL)
					}
					fmt.Fprintf(w, `{"id":%q,"content":"full source","document_references":[{"id":"doc"}]}`, item)
				}
			}))
			defer srv.Close()
			f, out := testFactory(srv)
			if err := runCmd(t, NewKnowledgeCmd(f), "context-pack", "ZEN-42", "--agent", agent, "--depth", "3", "--include", "build,passages", "--json"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), load) || !strings.Contains(out.String(), "loaded_at") {
				t.Fatalf("receipt lost: %s", out.String())
			}
			out.Reset()
			if err := runCmd(t, NewKnowledgeCmd(f), "get", item, "--context-load", load, "--json"); err != nil {
				t.Fatal(err)
			}
			if calls != 2 || !strings.Contains(out.String(), "full source") || !strings.Contains(out.String(), "document_references") {
				t.Fatalf("source lost: %s calls=%d", out.String(), calls)
			}
		})
	}
}

func TestKnowledgeContextReceiptsRejectInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"context-pack", "ZEN-1", "--agent", ""}, {"context-pack", "ZEN-1", "--agent", "browser"},
		{"get", "bad-id", "--context-load", "bad-id"},
		{"get", "bad-id", "--context-load", "11111111-1111-4111-8111-111111111111"},
		{"get", "11111111-1111-4111-8111-111111111111", "--context-load", "00000000-0000-0000-0000-000000000000"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid input reached server") }))
		f, out := testFactory(srv)
		if err := runCmd(t, NewKnowledgeCmd(f), args...); err == nil || out.Len() != 0 {
			t.Errorf("accepted %v", args)
		}
		srv.Close()
	}
}

func TestKnowledgeContextReceiptFailureDoesNotFallBackToUntrackedRead(t *testing.T) {
	for _, status := range []int{401, 403, 404, 429, 503} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls++
			w.WriteHeader(status)
			fmt.Fprint(w, `{"message":"receipt unavailable"}`)
		}))
		f, out := testFactory(srv)
		if err := runCmd(t, NewKnowledgeCmd(f), "context-pack", "ZEN-1", "--agent", "codex"); err == nil || out.Len() != 0 || calls != 1 {
			t.Errorf("failure hidden status=%d calls=%d out=%s err=%v", status, calls, out.String(), err)
		}
		srv.Close()
	}
}

func TestKnowledgeContextPackRejectsMissingOrMalformedReceipt(t *testing.T) {
	for _, body := range []string{`{}`, `{"load_id":"bad","loaded_at":"2026-10-26T10:00:00Z"}`, `{"load_id":"11111111-1111-4111-8111-111111111111"}`, `{"load_id":"11111111-1111-4111-8111-111111111111","loaded_at":"yesterday"}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) }))
		f, out := testFactory(srv)
		err := runCmd(t, NewKnowledgeCmd(f), "context-pack", "ZEN-1", "--agent", "codex")
		if err == nil || !strings.Contains(err.Error(), "valid context retrieval receipt") || out.Len() != 0 {
			t.Errorf("unconfirmed receipt accepted: %s / %v", out.String(), err)
		}
		srv.Close()
	}
}
