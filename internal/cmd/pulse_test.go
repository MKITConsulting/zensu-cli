package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const pulseTestSessionID = "11111111-1111-4111-8111-111111111111"

func TestPulseStart(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/pulse/sessions" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": pulseTestSessionID, "head_sha": "abc123"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123", "--branch", "main", "--project", "/repo", "--product", "p1"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	if body["headSha"] != "abc123" || body["branch"] != "main" || body["projectPath"] != "/repo" || body["productId"] != "p1" {
		t.Errorf("start body must carry headSha, branch, projectPath, productId: %v", body)
	}
	if !strings.Contains(out.String(), pulseTestSessionID) {
		t.Errorf("start output missing session id: %s", out.String())
	}
}

func TestPulseStart_RequiresHeadSha(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when --head-sha is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start"); err == nil {
		t.Fatal("pulse start without --head-sha should error")
	}
}

func TestPulseStart_TrackingDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "tracking_disabled"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	if got := out.String(); got != "Pulse tracking is disabled in Zensu; no session was created.\n" {
		t.Fatalf("start output = %q", got)
	}
}

func TestPulseStart_TrackingDisabledJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"tracking_disabled"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123", "--json"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	if got := out.String(); got != "{\n  \"status\": \"tracking_disabled\"\n}\n" {
		t.Fatalf("JSON output = %q", got)
	}
}

func TestPulseStart_EnabledJSONPreservesCompleteResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"` + pulseTestSessionID + `","head_sha":"abc123","organization_id":"org-1"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123", "--json"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	want := "{\n  \"id\": \"" + pulseTestSessionID + "\",\n  \"head_sha\": \"abc123\",\n  \"organization_id\": \"org-1\"\n}\n"
	if got := out.String(); got != want {
		t.Fatalf("JSON output = %q, want %q", got, want)
	}
}

func TestPulseStart_MinimalJSONRedactsSessionMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"` + pulseTestSessionID + `","user_id":"user-1","organization_id":"org-1","project_path":"/private/repo","branch":"secret"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123", "--minimal-json"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	want := "{\n  \"id\": \"" + pulseTestSessionID + "\"\n}\n"
	if got := out.String(); got != want {
		t.Fatalf("minimal JSON output = %q, want %q", got, want)
	}
}

func TestPulseStart_TrackingDisabledMinimalJSONRedactsMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"tracking_disabled","organization_id":"org-1","project_path":"/private/repo"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "start", "--head-sha", "abc123", "--minimal-json"); err != nil {
		t.Fatalf("pulse start error: %v", err)
	}
	if got := out.String(); got != "{\n  \"status\": \"tracking_disabled\"\n}\n" {
		t.Fatalf("minimal JSON output = %q", got)
	}
}

func TestPulseStart_RejectsMissingSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	err := runCmd(t, cmd, "start", "--head-sha", "abc123")
	if err == nil || !strings.Contains(err.Error(), "missing session id") {
		t.Fatalf("pulse start error = %v", err)
	}
}

func TestParsePulseCommandResponse_RejectsInvalidJSON(t *testing.T) {
	_, err := parsePulseCommandResponse([]byte(`{`))
	if err == nil || !strings.Contains(err.Error(), "invalid Pulse response") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestParsePulseCommandResponse_RejectsUnknownStatus(t *testing.T) {
	_, err := parsePulseCommandResponse([]byte(`{"status":"unexpected"}`))
	if err == nil || !strings.Contains(err.Error(), `unexpected Pulse response status "unexpected"`) {
		t.Fatalf("parse error = %v", err)
	}
}

func TestParsePulseCommandResponse_RejectsNonUUIDSessionID(t *testing.T) {
	_, err := parsePulseCommandResponse([]byte(`{"id":"not-a-uuid"}`))
	if err == nil || !strings.Contains(err.Error(), "expected canonical UUID") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestParsePulseCommandResponse_RejectsDisabledResponseWithSessionID(t *testing.T) {
	_, err := parsePulseCommandResponse([]byte(`{"status":"tracking_disabled","id":"` + pulseTestSessionID + `"}`))
	if err == nil || !strings.Contains(err.Error(), "tracking_disabled must not include a session id") {
		t.Fatalf("parse error = %v", err)
	}
}

func TestPulseEnd(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/pulse/sessions/"+pulseTestSessionID+"/end" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": pulseTestSessionID, "ended_at": "2026-01-01T00:00:00Z"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID, "--changed-files", "a.go, b.go"); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	files, ok := body["changedFiles"].([]any)
	if !ok || len(files) != 2 || files[0] != "a.go" || files[1] != "b.go" {
		t.Errorf("end body must carry trimmed changedFiles array: %v", body["changedFiles"])
	}
	if !strings.Contains(out.String(), pulseTestSessionID) {
		t.Errorf("end output missing session id: %s", out.String())
	}
}

func TestPulseEnd_ChangedFileFlagsPreservePathsLosslessly(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": pulseTestSessionID})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd,
		"end", pulseTestSessionID,
		"--changed-file", "src/with,comma.go",
		"--changed-file", " leading-and-trailing.go ",
	); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}

	files, ok := body["changedFiles"].([]any)
	if !ok || len(files) != 2 || files[0] != "src/with,comma.go" || files[1] != " leading-and-trailing.go " {
		t.Fatalf("repeated --changed-file values must be preserved losslessly: %v", body["changedFiles"])
	}
}

func TestPulseEnd_NoChangedFilesSendsEmptyArray(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": pulseTestSessionID})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	files, ok := body["changedFiles"].([]any)
	if !ok || len(files) != 0 {
		t.Fatalf("changedFiles = %#v, want non-null empty array", body["changedFiles"])
	}
}

func TestPulseEnd_RequiresSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called when session id arg is missing")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end"); err == nil {
		t.Fatal("pulse end without a session id should error")
	}
}

func TestPulseCommands_RejectInvalidSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for an invalid session id")
	}))
	defer srv.Close()

	for _, args := range [][]string{{"end", "not-a-uuid"}, {"summary", "not-a-uuid"}} {
		f, _ := testFactory(srv)
		cmd := NewPulseCmd(f)
		err := runCmd(t, cmd, args...)
		if err == nil || !strings.Contains(err.Error(), "expected canonical UUID") {
			t.Fatalf("%v error = %v", args, err)
		}
	}
}

func TestPulseEnd_TrackingDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "tracking_disabled"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	if got := out.String(); got != "Pulse tracking is disabled in Zensu; no session end was recorded.\n" {
		t.Fatalf("end output = %q", got)
	}
}

func TestPulseEnd_TrackingDisabledJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"tracking_disabled"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID, "--json"); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	if got := out.String(); got != "{\n  \"status\": \"tracking_disabled\"\n}\n" {
		t.Fatalf("JSON output = %q", got)
	}
}

func TestPulseEnd_TrackingDisabledMinimalJSONRedactsMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"tracking_disabled","changed_files":["secret.go"],"organization_id":"org-1"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID, "--minimal-json"); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	if got := out.String(); got != "{\n  \"status\": \"tracking_disabled\"\n}\n" {
		t.Fatalf("minimal JSON output = %q", got)
	}
}

func TestPulseEnd_EnabledJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"` + pulseTestSessionID + `","ended_at":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID, "--json"); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	want := "{\n  \"id\": \"" + pulseTestSessionID + "\",\n  \"ended_at\": \"2026-01-01T00:00:00Z\"\n}\n"
	if got := out.String(); got != want {
		t.Fatalf("JSON output = %q, want %q", got, want)
	}
}

func TestPulseEnd_MinimalJSONRedactsSessionMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"` + pulseTestSessionID + `","changed_files":["secret.go"],"features_touched":["feature-1"]}`))
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "end", pulseTestSessionID, "--minimal-json"); err != nil {
		t.Fatalf("pulse end error: %v", err)
	}
	want := "{\n  \"id\": \"" + pulseTestSessionID + "\"\n}\n"
	if got := out.String(); got != want {
		t.Fatalf("minimal JSON output = %q, want %q", got, want)
	}
}

func TestPulseEnd_RejectsMismatchedResponseID(t *testing.T) {
	const otherSessionID = "22222222-2222-4222-8222-222222222222"

	for _, tc := range []struct {
		name      string
		flags     []string
		wantError string
		redactIDs bool
	}{
		{name: "human", wantError: "does not match requested session id"},
		{name: "public JSON", flags: []string{"--json"}, wantError: "does not match requested session id"},
		{name: "minimal JSON", flags: []string{"--minimal-json"}, wantError: errInvalidPulseResponse.Error(), redactIDs: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"id":"` + otherSessionID + `"}`))
			}))
			defer srv.Close()

			f, out := testFactory(srv)
			cmd := NewPulseCmd(f)
			args := append([]string{"end", pulseTestSessionID}, tc.flags...)
			err := runCmd(t, cmd, args...)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("pulse end error = %v, want %q", err, tc.wantError)
			}
			if got := out.String(); got != "" {
				t.Fatalf("output before ID validation = %q", got)
			}
			if tc.redactIDs && (strings.Contains(err.Error(), pulseTestSessionID) || strings.Contains(err.Error(), otherSessionID)) {
				t.Fatalf("minimal error leaked a session id: %v", err)
			}
		})
	}
}

func TestPulseMinimalJSONErrorsDoNotReflectRemoteData(t *testing.T) {
	const sentinel = "sensitive-remote-value"

	for _, tc := range []struct {
		name      string
		args      []string
		status    int
		body      string
		wantError error
	}{
		{
			name:      "start request failure",
			args:      []string{"start", "--head-sha", "abc123", "--minimal-json"},
			status:    http.StatusBadRequest,
			body:      `{"message":"` + sentinel + `"}`,
			wantError: errPulseRequestFailed,
		},
		{
			name:      "end request failure",
			args:      []string{"end", pulseTestSessionID, "--minimal-json"},
			status:    http.StatusInternalServerError,
			body:      sentinel,
			wantError: errPulseRequestFailed,
		},
		{
			name:      "start malformed success",
			args:      []string{"start", "--head-sha", "abc123", "--minimal-json"},
			status:    http.StatusOK,
			body:      `{"status":"` + sentinel + `"}`,
			wantError: errInvalidPulseResponse,
		},
		{
			name:      "end malformed success",
			args:      []string{"end", pulseTestSessionID, "--minimal-json"},
			status:    http.StatusOK,
			body:      `{"id":"` + sentinel + `"}`,
			wantError: errInvalidPulseResponse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			f, out := testFactory(srv)
			cmd := NewPulseCmd(f)
			err := runCmd(t, cmd, tc.args...)
			if err == nil || err.Error() != tc.wantError.Error() {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
			if strings.Contains(err.Error(), sentinel) || strings.Contains(out.String(), sentinel) {
				t.Fatalf("minimal mode leaked remote data: err=%v out=%q", err, out.String())
			}
		})
	}
}

func TestPulseRequestErrorsRemainDetailedOutsideMinimalJSON(t *testing.T) {
	const sentinel = "detailed-request-error"

	for _, tc := range []struct {
		name string
		flag string
	}{
		{name: "human"},
		{name: "public JSON", flag: "--json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"` + sentinel + `"}`))
			}))
			defer srv.Close()

			f, out := testFactory(srv)
			cmd := NewPulseCmd(f)
			args := []string{"start", "--head-sha", "abc123"}
			if tc.flag != "" {
				args = append(args, tc.flag)
			}
			err := runCmd(t, cmd, args...)
			if err == nil || !strings.Contains(err.Error(), sentinel) || !strings.Contains(err.Error(), "status 400") {
				t.Fatalf("error = %v, want detailed request error", err)
			}
			if errors.Is(err, errPulseRequestFailed) {
				t.Fatalf("non-minimal mode replaced detailed error: %v", err)
			}
			if got := out.String(); got != "" {
				t.Fatalf("output on request failure = %q", got)
			}
		})
	}
}

func TestPulseJSONFlagsAreMutuallyExclusive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server must not be called for mutually exclusive flags")
	}))
	defer srv.Close()

	for _, args := range [][]string{
		{"start", "--head-sha", "abc123", "--json", "--minimal-json"},
		{"end", pulseTestSessionID, "--json", "--minimal-json"},
	} {
		f, out := testFactory(srv)
		cmd := NewPulseCmd(f)
		if err := runCmd(t, cmd, args...); err == nil {
			t.Fatalf("%v should reject mutually exclusive JSON flags", args)
		}
		if got := out.String(); got != "" {
			t.Fatalf("%v output = %q", args, got)
		}
	}
}

func TestPulseSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/pulse/sessions/"+pulseTestSessionID+"/summary" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session":   map[string]any{"id": pulseTestSessionID},
			"toolCalls": []map[string]any{{"id": "t1", "tool_name": "create_feature"}},
		})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewPulseCmd(f)
	if err := runCmd(t, cmd, "summary", pulseTestSessionID); err != nil {
		t.Fatalf("pulse summary error: %v", err)
	}
	if !strings.Contains(out.String(), "create_feature") {
		t.Errorf("summary output missing tool call: %s", out.String())
	}
}
