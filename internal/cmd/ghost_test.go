package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
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

func ghostCandidateFixture(n int) []map[string]any {
	candidates := make([]map[string]any, n)
	for i := range candidates {
		candidates[i] = map[string]any{
			"id":              fmt.Sprintf("cand-%03d", i),
			"candidate_slug":  fmt.Sprintf("ZEN-%03d", i),
			"candidate_title": fmt.Sprintf("Candidate %03d", i),
		}
	}
	return candidates
}

func ghostCandidateIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("cand-%03d", i)
	}
	return ids
}

func ghostCandidatePages(n int) func(recordedRequest) fakeReply {
	respond := backendPages(ghostCandidateFixture(n), 100)
	return func(r recordedRequest) fakeReply {
		page, _ := strconv.Atoi(r.Query.Get("page"))
		perPage, _ := strconv.Atoi(r.Query.Get("per_page"))
		status, body := respond(page, perPage)
		return fakeReply{Status: status, Body: body}
	}
}

func legacyGhostCandidates(n int) func(recordedRequest) fakeReply {
	candidates := ghostCandidateFixture(n)
	return func(r recordedRequest) fakeReply {
		limit := 50
		if v, err := strconv.Atoi(r.Query.Get("limit")); err == nil && v > 0 && v <= legacyGhostCandidateLimit {
			limit = v
		}
		return fakeReply{Body: candidates[:min(limit, len(candidates))]}
	}
}

func candidatePageQuery(page int) url.Values {
	return url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}
}

func TestGhostCandidates_ReadsEveryPageAndFallsBackToTheLegacyArray(t *testing.T) {
	listFailure := reply(http.StatusInternalServerError, map[string]any{"code": "internal_error", "message": "failed to list ghost candidates"})
	tests := []struct {
		name           string
		respond        func(recordedRequest) fakeReply
		wantQueries    []url.Values
		wantCandidates int
		wantErr        string
	}{
		{
			name:           "an envelope list of 230 candidates is read over three requests",
			respond:        ghostCandidatePages(230),
			wantQueries:    []url.Values{candidatePageQuery(1), candidatePageQuery(2), candidatePageQuery(3)},
			wantCandidates: 230,
		},
		{
			name:           "a full scan of 200 candidates is read over two requests",
			respond:        ghostCandidatePages(legacyGhostCandidateLimit),
			wantQueries:    []url.Values{candidatePageQuery(1), candidatePageQuery(2)},
			wantCandidates: legacyGhostCandidateLimit,
		},
		{
			name:        "an empty envelope list prints an empty array",
			respond:     ghostCandidatePages(0),
			wantQueries: []url.Values{candidatePageQuery(1)},
		},
		{
			name:           "a legacy array is read again with the old limit and printed unchanged",
			respond:        legacyGhostCandidates(legacyGhostCandidateLimit - 1),
			wantQueries:    []url.Values{candidatePageQuery(1), {"limit": {"200"}}},
			wantCandidates: legacyGhostCandidateLimit - 1,
		},
		{
			name:           "a full legacy scan of 200 candidates is complete, so it prints unchanged without a warning",
			respond:        legacyGhostCandidates(legacyGhostCandidateLimit),
			wantQueries:    []url.Values{candidatePageQuery(1), {"limit": {"200"}}},
			wantCandidates: legacyGhostCandidateLimit,
		},
		{
			name:        "an error status on the first page reaches the user unwrapped",
			respond:     reply(http.StatusNotFound, map[string]any{"code": "not_found", "message": "ghost scan not found"}),
			wantQueries: []url.Values{candidatePageQuery(1)},
			wantErr:     "ghost scan not found (status 404, code not_found)",
		},
		{
			name: "an error status on a later page names that page",
			respond: func(r recordedRequest) fakeReply {
				if r.Query.Get("page") == "2" {
					return listFailure(r)
				}
				return ghostCandidatePages(230)(r)
			},
			wantQueries: []url.Values{candidatePageQuery(1), candidatePageQuery(2)},
			wantErr:     "reading page 2 of the ghost candidates list: failed to list ghost candidates (status 500, code internal_error)",
		},
		{
			name: "an error status on the legacy read reaches the user unwrapped",
			respond: func(r recordedRequest) fakeReply {
				if r.Query.Has("limit") {
					return listFailure(r)
				}
				return legacyGhostCandidates(legacyGhostCandidateLimit)(r)
			},
			wantQueries: []url.Values{candidatePageQuery(1), {"limit": {"200"}}},
			wantErr:     "failed to list ghost candidates (status 500, code internal_error)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"GET /api/products/p1/ghost/scans/s1/candidates": tc.respond,
			})
			f, out := testFactory(srv)
			cmd := NewGhostCmd(f)
			var stderr bytes.Buffer
			cmd.SetErr(&stderr)
			err := runCmd(t, cmd, "candidates", "s1", "--product", "p1")
			var gotQueries []url.Values
			for _, call := range api.calls() {
				gotQueries = append(gotQueries, call.Query)
			}
			if !reflect.DeepEqual(gotQueries, tc.wantQueries) {
				t.Errorf("the requests sent the queries %v, want %v", gotQueries, tc.wantQueries)
			}
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error: got %v, want %q", err, tc.wantErr)
				}
				if out.Len() != 0 {
					t.Errorf("a failed listing must not print a partial list, got:\n%s", out.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("ghost candidates error: %v", err)
			}
			if stderr.Len() != 0 {
				t.Errorf("a scan holds at most 200 candidates, so every listing is complete and needs no warning, got stderr %q", stderr.String())
			}
			var got []map[string]any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout must be one JSON array of candidates: %v\n%s", err, out.String())
			}
			ids := make([]string, 0, len(got))
			for _, candidate := range got {
				id, _ := candidate["id"].(string)
				ids = append(ids, id)
			}
			if want := ghostCandidateIDs(tc.wantCandidates); !slices.Equal(ids, want) {
				t.Errorf("stdout lists the ids %v, want each id once in server order: %v", ids, want)
			}
			var want []map[string]any
			if err := json.Unmarshal([]byte(jsonOf(t, ghostCandidateFixture(tc.wantCandidates))), &want); err != nil {
				t.Fatalf("decode the expected JSON: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("stdout must hold the server's candidates unchanged, got:\n%s", out.String())
			}
		})
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
