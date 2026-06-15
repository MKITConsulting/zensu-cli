package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityClassify(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/features/f1/security" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "f1"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "classify", "f1", "--classification", "confidential", "--auth-required", "--rate-limited=false"); err != nil {
		t.Fatalf("security classify error: %v", err)
	}
	if body["securityClassification"] != "confidential" {
		t.Errorf("classify body securityClassification: got %v want confidential", body["securityClassification"])
	}
	if body["authRequired"] != true {
		t.Errorf("classify body authRequired: got %v want true", body["authRequired"])
	}
	if body["rateLimited"] != false {
		t.Errorf("classify must send an explicitly-set false bool: got %v want false", body["rateLimited"])
	}
	if _, ok := body["auditLogged"]; ok {
		t.Errorf("classify must not send unset bool flags: auditLogged present = %v", body["auditLogged"])
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("classify output missing feature id: %s", out.String())
	}
}

func TestSecurityClassify_RequiresAField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when no classify flags are passed")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "classify", "f1"); err == nil {
		t.Fatal("security classify with no flags should error before any request")
	}
}

func TestSecurityPosture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products/p1/security" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"averageScore": 7.5, "featureCount": 3})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "posture", "p1"); err != nil {
		t.Fatalf("security posture error: %v", err)
	}
	if !strings.Contains(out.String(), "averageScore") {
		t.Errorf("posture output missing data: %s", out.String())
	}
}

func TestSecurityScore(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/security/score" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"score": 8.0, "allowed": true})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "score", "f1"); err != nil {
		t.Fatalf("security score error: %v", err)
	}
	if !strings.Contains(out.String(), "score") {
		t.Errorf("score output missing data: %s", out.String())
	}
}

func TestSecurityAddTest(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/security/tests" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "t9"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "add-test", "f1", "--type", "injection", "--file", "tests/sec/injection_test.go", "--owasp-id", "A03:2021"); err != nil {
		t.Fatalf("security add-test error: %v", err)
	}
	if body["securityTestType"] != "injection" {
		t.Errorf("add-test body securityTestType: got %v want injection", body["securityTestType"])
	}
	if body["filePath"] != "tests/sec/injection_test.go" {
		t.Errorf("add-test body filePath: got %v", body["filePath"])
	}
	if body["owaspId"] != "A03:2021" {
		t.Errorf("add-test body owaspId: got %v want A03:2021", body["owaspId"])
	}
	if !strings.Contains(out.String(), "f1") {
		t.Errorf("add-test output missing feature id: %s", out.String())
	}
}

func TestSecurityAddTest_RequiresTypeAndFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --type/--file are missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "add-test", "f1", "--type", "injection"); err == nil {
		t.Fatal("security add-test without --file should error before any request")
	}
}

func TestSecurityReview(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/features/f1/security/review" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "r9"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "review", "f1", "--reviewer", "alice", "--status", "approved"); err != nil {
		t.Fatalf("security review error: %v", err)
	}
	if body["reviewer"] != "alice" {
		t.Errorf("review body reviewer: got %v want alice", body["reviewer"])
	}
	if body["reviewStatus"] != "approved" {
		t.Errorf("review body reviewStatus: got %v want approved", body["reviewStatus"])
	}
	if body["reviewType"] != "manual" {
		t.Errorf("review body reviewType should default to manual: got %v", body["reviewType"])
	}
	if !strings.Contains(out.String(), "approved") {
		t.Errorf("review output should confirm status: %s", out.String())
	}
}

func TestSecurityReview_RequiresReviewerAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --reviewer/--status are missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "review", "f1", "--reviewer", "alice"); err == nil {
		t.Fatal("security review without --status should error before any request")
	}
}

func TestSecurityAnalyze(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/security-context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"currentClassification": "internal", "score": 6.0})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "analyze", "f1"); err != nil {
		t.Fatalf("security analyze error: %v", err)
	}
	if !strings.Contains(out.String(), "currentClassification") {
		t.Errorf("analyze output missing security context: %s", out.String())
	}
}

func TestSecurityValidate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/security-context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"score": 9.0})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "validate", "f1"); err != nil {
		t.Fatalf("security validate error: %v", err)
	}
	if !strings.Contains(out.String(), "score") {
		t.Errorf("validate output missing data: %s", out.String())
	}
}

func TestSecuritySuggestTests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/security-context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"securityRequirements": map[string]string{}})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "suggest-tests", "f1"); err != nil {
		t.Fatalf("security suggest-tests error: %v", err)
	}
	if !strings.Contains(out.String(), "securityRequirements") {
		t.Errorf("suggest-tests output missing context: %s", out.String())
	}
}

func TestSecurityThreatModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/features/f1/security-context" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"threatModel": nil})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewSecurityCmd(f)
	if err := runCmd(t, cmd, "threat-model", "f1"); err != nil {
		t.Fatalf("security threat-model error: %v", err)
	}
	if !strings.Contains(out.String(), "threatModel") {
		t.Errorf("threat-model output missing context: %s", out.String())
	}
}
