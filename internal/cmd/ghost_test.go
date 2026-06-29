package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGhostScan(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/ghost/scans" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"scan": map[string]any{"id": "s9", "candidates_total": 2},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "scan", "--product", "p1", "--candidates", `[{"slug":"a","title":"A"}]`); err != nil {
		t.Fatalf("ghost scan error: %v", err)
	}
	cands, ok := body["candidates"].([]any)
	if !ok || len(cands) != 1 {
		t.Errorf("scan body must carry candidates array: %v", body["candidates"])
	}
	if body["source"] != "api" {
		t.Errorf("scan body must default source=api: %v", body["source"])
	}
	if !strings.Contains(out.String(), "s9") {
		t.Errorf("scan output missing scan id: %s", out.String())
	}
}

func TestGhostScan_WithOptionalFlags(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"scan": map[string]any{"id": "s9"}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "scan", "--product", "p1",
		"--candidates", `[{"slug":"a","title":"A"}]`,
		"--components", `[{"slug":"c","name":"C"}]`,
		"--repo-url", "https://example.com/repo",
		"--branch", "main"); err != nil {
		t.Fatalf("ghost scan error: %v", err)
	}
	if _, ok := body["components"].([]any); !ok {
		t.Errorf("scan body must carry components array: %v", body["components"])
	}
	if body["repoUrl"] != "https://example.com/repo" {
		t.Errorf("scan body must carry repoUrl: %v", body["repoUrl"])
	}
	if body["branch"] != "main" {
		t.Errorf("scan body must carry branch: %v", body["branch"])
	}
}

func TestGhostScan_SourceFlag(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"scan": map[string]any{"id": "s9"}})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "scan", "--product", "p1",
		"--candidates", `[{"slug":"a","title":"A"}]`,
		"--source", "web_ui"); err != nil {
		t.Fatalf("ghost scan error: %v", err)
	}
	if body["source"] != "web_ui" {
		t.Errorf("scan body must carry source=web_ui: %v", body["source"])
	}
}

func TestGhostScan_RejectsInvalidSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --source is invalid")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "scan", "--product", "p1",
		"--candidates", `[{"slug":"a","title":"A"}]`,
		"--source", "cli"); err == nil {
		t.Fatal("ghost scan with invalid --source should error")
	}
}

func TestGhostScan_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "scan", "--candidates", `[{"slug":"a","title":"A"}]`); err == nil {
		t.Fatal("ghost scan without --product should error")
	}
}

func TestGhostScan_RequiresCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --candidates is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "scan", "--product", "p1"); err == nil {
		t.Fatal("ghost scan without --candidates should error")
	}
}

func TestGhostCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/ghost/scans/s1/candidates" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"id": "cand1", "candidate_slug": "ZEN-001", "candidate_title": "Login"},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "candidates", "s1", "--product", "p1"); err != nil {
		t.Fatalf("ghost candidates error: %v", err)
	}
	if !strings.Contains(out.String(), "cand1") {
		t.Errorf("candidates output missing candidate id: %s", out.String())
	}
}

func TestGhostCandidates_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "candidates", "s1"); err == nil {
		t.Fatal("ghost candidates without --product should error")
	}
}

func TestGhostApprove(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/ghost/scans/s1/candidates/cand1/approve" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "cand1", "review_status": "approved"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "approve", "s1", "cand1", "--product", "p1"); err != nil {
		t.Fatalf("ghost approve error: %v", err)
	}
	if !called {
		t.Error("approve must call the server")
	}
	if !strings.Contains(out.String(), "cand1") {
		t.Errorf("approve output should confirm candidate: %s", out.String())
	}
}

func TestGhostApprove_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "approve", "s1", "cand1"); err == nil {
		t.Fatal("ghost approve without --product should error")
	}
}

func TestGhostReject(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/ghost/scans/s1/candidates/cand1/reject" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "cand1", "review_status": "rejected"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "reject", "s1", "cand1", "--product", "p1", "--reason", "duplicate"); err != nil {
		t.Fatalf("ghost reject error: %v", err)
	}
	if body["reason"] != "duplicate" {
		t.Errorf("reject body must carry reason: %v", body["reason"])
	}
	if !strings.Contains(out.String(), "cand1") {
		t.Errorf("reject output should confirm candidate: %s", out.String())
	}
}

func TestGhostReject_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "reject", "s1", "cand1"); err == nil {
		t.Fatal("ghost reject without --product should error")
	}
}

func TestGhostBatch(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/ghost/scans/s1/batch-review" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"approved": 2, "rejected": 1})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "batch", "s1", "--product", "p1",
		"--approve-ids", `["a1","a2"]`,
		"--reject-ids", `["r1"]`,
		"--reject-reason", "low confidence"); err != nil {
		t.Fatalf("ghost batch error: %v", err)
	}
	approve, ok := body["approve"].([]any)
	if !ok || len(approve) != 2 {
		t.Errorf("batch body must carry approve array of 2: %v", body["approve"])
	}
	reject, ok := body["reject"].([]any)
	if !ok || len(reject) != 1 {
		t.Fatalf("batch body must carry reject array of 1: %v", body["reject"])
	}
	rejItem, _ := reject[0].(map[string]any)
	if rejItem["id"] != "r1" || rejItem["reason"] != "low confidence" {
		t.Errorf("reject item must carry id+reason: %v", rejItem)
	}
	if !strings.Contains(out.String(), "2 approved") {
		t.Errorf("batch output should summarize counts: %s", out.String())
	}
}

func TestGhostBatch_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "batch", "s1", "--approve-ids", `["a1"]`); err == nil {
		t.Fatal("ghost batch without --product should error")
	}
}

func TestGhostBatch_RequiresIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no approve/reject ids are given")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "batch", "s1", "--product", "p1"); err == nil {
		t.Fatal("ghost batch without any ids should error")
	}
}

func TestGhostApply(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/products/p1/ghost/scans/s1/apply" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"featuresCreated": 3, "featuresEnriched": 1, "componentsCreated": 2,
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "apply", "s1", "--product", "p1"); err != nil {
		t.Fatalf("ghost apply error: %v", err)
	}
	if !strings.Contains(out.String(), "3 features created") {
		t.Errorf("apply output should summarize results: %s", out.String())
	}
}

func TestGhostApply_EnrichExistingQuery(t *testing.T) {
	var gotEnrich string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEnrich = r.URL.Query().Get("enrich_existing")
		_ = json.NewEncoder(w).Encode(map[string]any{"featuresCreated": 0})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "apply", "s1", "--product", "p1", "--enrich-existing"); err != nil {
		t.Fatalf("ghost apply error: %v", err)
	}
	if gotEnrich != "true" {
		t.Errorf("apply --enrich-existing must set enrich_existing=true query: got %q", gotEnrich)
	}
}

func TestGhostApply_RequiresProduct(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --product is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewGhostCmd(f)
	if err := runCmd(t, cmd, "apply", "s1"); err == nil {
		t.Fatal("ghost apply without --product should error")
	}
}
