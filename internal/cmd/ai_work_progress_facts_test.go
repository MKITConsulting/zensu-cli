package cmd

import (
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var contractPayloadFields = map[string][]string{
	"session_started":       {"branch", "clientVersion", "harness", "worktree"},
	"session_ended":         {"reason"},
	"turn_started":          {},
	"turn_ended":            {},
	"segment_started":       {"phase"},
	"segment_ended":         {"outcome", "phase"},
	"step_progress":         {"done", "label", "total"},
	"wait_requested":        {"subject"},
	"wait_resolved":         {"outcome", "subject"},
	"review_round_started":  {"round"},
	"review_round_ended":    {"chainClosed", "findings.blocking", "findings.fixed", "findings.open", "items.severity", "items.state", "items.title", "limitReached", "round"},
	"change_request_linked": {"number", "repository", "state", "url"},
	"workspace_state":       {"commitsAhead", "filesChanged", "head", "uncommittedFiles"},
	"evidence_reported":     {"check", "failed", "outcome", "passed", "percent"},
	"blocked":               {"kind", "reason"},
	"resumed":               {},
	"signal":                {"activity"},
	"work_ended":            {"reason"},
}

var forbiddenPayloadWords = []string{"prompt", "answer", "question", "command", "code", "diff", "path", "file", "host", "token", "transcript"}

func payloadFieldNames(t reflect.Type, prefix string, out *[]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			*out = append(*out, prefix+"<untagged:"+field.Name+">")
			continue
		}
		inner := field.Type
		for inner.Kind() == reflect.Pointer || inner.Kind() == reflect.Slice {
			inner = inner.Elem()
		}
		if inner.Kind() == reflect.Struct {
			payloadFieldNames(inner, prefix+name+".", out)
			continue
		}
		*out = append(*out, prefix+name)
	}
}

func TestProgressPayloadTypes_CarryExactlyTheContractFields(t *testing.T) {
	if len(progressFactKinds) != 18 || len(contractPayloadFields) != 18 || len(progressKindFlags) != 18 {
		t.Fatalf("kinds=%d contract table=%d flag table=%d, want 18 each", len(progressFactKinds), len(contractPayloadFields), len(progressKindFlags))
	}
	for _, kind := range progressFactKinds {
		want, listed := contractPayloadFields[kind]
		if !listed {
			t.Errorf("kind %s is missing from the contract table of this test", kind)
			continue
		}
		payload := newProgressPayload(kind)
		if payload == nil {
			t.Errorf("kind %s has no payload type", kind)
			continue
		}
		got := []string{}
		payloadFieldNames(reflect.TypeOf(payload), "", &got)
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("%s payload fields = %q, want the contract's %q", kind, got, want)
		}
		for _, field := range got {
			lower := strings.ToLower(field)
			for _, word := range forbiddenPayloadWords {
				if strings.Contains(lower, word) && field != "filesChanged" && field != "uncommittedFiles" {
					t.Errorf("%s payload field %q names content that must never leave the workstation (%s)", kind, field, word)
				}
			}
		}
	}
	if newProgressPayload("heartbeat") != nil {
		t.Error("an unknown kind must have no payload type")
	}
}

func flagsFor(set map[string]bool) func(string) bool {
	return func(name string) bool { return set[name] }
}

func TestProgressFactFromFlags_BuildsTheContractPayloadOfEveryKind(t *testing.T) {
	for _, tc := range []struct {
		kind    string
		values  progressFlagValues
		changed []string
		want    string
	}{
		{"session_started", progressFlagValues{harness: "claude_code", clientVersion: "0.41.0", branch: "claude/work-status", worktree: "practical-poincare"}, []string{"harness", "client-version", "branch", "worktree"}, `{"harness":"claude_code","clientVersion":"0.41.0","branch":"claude/work-status","worktree":"practical-poincare"}`},
		{"session_started", progressFlagValues{harness: "other"}, []string{"harness"}, `{"harness":"other"}`},
		{"session_ended", progressFlagValues{reason: "interrupted"}, []string{"reason"}, `{"reason":"interrupted"}`},
		{"turn_started", progressFlagValues{}, nil, ``},
		{"turn_ended", progressFlagValues{}, nil, ``},
		{"resumed", progressFlagValues{}, nil, ``},
		{"segment_started", progressFlagValues{phase: "plan"}, []string{"phase"}, `{"phase":"plan"}`},
		{"segment_ended", progressFlagValues{phase: "implement", outcome: "completed"}, []string{"phase", "outcome"}, `{"phase":"implement","outcome":"completed"}`},
		{"step_progress", progressFlagValues{done: 4, total: 7, label: "  Wire the handler  "}, []string{"done", "total", "label"}, `{"done":4,"total":7,"label":"Wire the handler"}`},
		{"step_progress", progressFlagValues{done: 0, total: 1}, []string{"done", "total"}, `{"done":0,"total":1}`},
		{"wait_requested", progressFlagValues{subject: "plan_approval"}, []string{"subject"}, `{"subject":"plan_approval"}`},
		{"wait_resolved", progressFlagValues{subject: "permission", outcome: "granted"}, []string{"subject", "outcome"}, `{"subject":"permission","outcome":"granted"}`},
		{"review_round_started", progressFlagValues{round: 2}, []string{"round"}, `{"round":2}`},
		{"review_round_ended", progressFlagValues{round: 2, findingsOpen: 3, findingsFixed: 1, findingsBlocking: 1, findings: []string{"high:open:Token leaks: in the log", "low:fixed:Typo"}, chainClosed: true, limitReached: true}, []string{"round", "findings-open", "findings-fixed", "findings-blocking", "finding", "chain-closed", "limit-reached"}, `{"round":2,"findings":{"open":3,"fixed":1,"blocking":1},"items":[{"severity":"high","state":"open","title":"Token leaks: in the log"},{"severity":"low","state":"fixed","title":"Typo"}],"chainClosed":true,"limitReached":true}`},
		{"review_round_ended", progressFlagValues{round: 1}, []string{"round"}, `{"round":1,"findings":{"open":0,"fixed":0,"blocking":0}}`},
		{"change_request_linked", progressFlagValues{repository: "MKITConsulting/zensu-cli", number: 57, state: "open", url: "https://github.com/MKITConsulting/zensu-cli/pull/57"}, []string{"repository", "number", "state", "url"}, `{"repository":"MKITConsulting/zensu-cli","number":57,"state":"open","url":"https://github.com/MKITConsulting/zensu-cli/pull/57"}`},
		{"change_request_linked", progressFlagValues{repository: "group/sub/project", number: 1}, []string{"repository", "number"}, `{"repository":"group/sub/project","number":1}`},
		{"workspace_state", progressFlagValues{commitsAhead: 2, filesChanged: 9, uncommittedFiles: 1, head: "BC15472ABC"}, []string{"commits-ahead", "files-changed", "uncommitted-files", "head"}, `{"commitsAhead":2,"filesChanged":9,"uncommittedFiles":1,"head":"bc15472abc"}`},
		{"evidence_reported", progressFlagValues{check: "coverage", outcome: "passed", passed: 0, failed: 3, percent: 91.5}, []string{"check", "outcome", "passed", "failed", "percent"}, `{"check":"coverage","outcome":"passed","passed":0,"failed":3,"percent":91.5}`},
		{"evidence_reported", progressFlagValues{check: "lint", outcome: "skipped"}, []string{"check", "outcome"}, `{"check":"lint","outcome":"skipped"}`},
		{"blocked", progressFlagValues{blockKind: "precondition", reason: "Waiting for the API key"}, []string{"block-kind", "reason"}, `{"kind":"precondition","reason":"Waiting for the API key"}`},
		{"blocked", progressFlagValues{blockKind: "other"}, []string{"block-kind"}, `{"kind":"other"}`},
		{"signal", progressFlagValues{activity: "testing"}, []string{"activity"}, `{"activity":"testing"}`},
		{"signal", progressFlagValues{}, nil, ``},
		{"work_ended", progressFlagValues{reason: "handed_off"}, []string{"reason"}, `{"reason":"handed_off"}`},
	} {
		set := map[string]bool{}
		for _, name := range tc.changed {
			set[name] = true
		}
		fact, err := progressFactFromFlags(tc.kind, &tc.values, flagsFor(set))
		if err != nil {
			t.Errorf("%s %v: %v", tc.kind, tc.changed, err)
			continue
		}
		if fact.Kind != tc.kind || string(fact.Payload) != tc.want {
			t.Errorf("%s payload = %s, want %s", tc.kind, fact.Payload, tc.want)
		}
	}
}

func TestProgressFactFromFlags_RefusesWhatTheContractRefuses(t *testing.T) {
	long := strings.Repeat("ä", 121)
	nan := math.NaN()
	for _, tc := range []struct {
		name    string
		kind    string
		values  progressFlagValues
		changed []string
		want    string
	}{
		{"unknown kind", "heartbeat", progressFlagValues{}, nil, `unknown --kind "heartbeat"`},
		{"flag of another kind", "signal", progressFlagValues{done: 1}, []string{"done"}, "--done does not belong to kind signal"},
		{"flag on a kind without fields", "turn_started", progressFlagValues{reason: "x"}, []string{"reason"}, "--reason does not belong to kind turn_started"},
		{"harness missing", "session_started", progressFlagValues{}, nil, "harness must be one of"},
		{"unknown harness", "session_started", progressFlagValues{harness: "cursor"}, []string{"harness"}, "harness must be one of"},
		{"worktree is a path", "session_started", progressFlagValues{harness: "codex", worktree: "work/trees"}, []string{"harness", "worktree"}, "worktree must be a label"},
		{"worktree with a backslash", "session_started", progressFlagValues{harness: "codex", worktree: `C:\work`}, []string{"harness", "worktree"}, "worktree must be a label"},
		{"worktree too long", "session_started", progressFlagValues{harness: "codex", worktree: strings.Repeat("w", 101)}, []string{"harness", "worktree"}, "worktree must be a label"},
		{"client version with a space", "session_started", progressFlagValues{harness: "codex", clientVersion: "1.0 beta"}, []string{"harness", "client-version"}, "clientVersion must be"},
		{"branch with two dots", "session_started", progressFlagValues{harness: "codex", branch: "a..b"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch with a space", "session_started", progressFlagValues{harness: "codex", branch: "my branch"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch starting with a hyphen", "session_started", progressFlagValues{harness: "codex", branch: "-x"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch ending in .lock", "session_started", progressFlagValues{harness: "codex", branch: "a/b.lock"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch with a trailing slash", "session_started", progressFlagValues{harness: "codex", branch: "a/"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch that is only @", "session_started", progressFlagValues{harness: "codex", branch: "@"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch with @{", "session_started", progressFlagValues{harness: "codex", branch: "a@{b"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"branch with a tilde", "session_started", progressFlagValues{harness: "codex", branch: "a~1"}, []string{"harness", "branch"}, "branch must be a git branch name"},
		{"unknown session end reason", "session_ended", progressFlagValues{reason: "crashed"}, []string{"reason"}, "reason must be one of ended, interrupted, unknown"},
		{"unknown phase", "segment_started", progressFlagValues{phase: "deploy"}, []string{"phase"}, "phase must be one of"},
		{"unknown segment outcome", "segment_ended", progressFlagValues{phase: "plan", outcome: "approved"}, []string{"phase", "outcome"}, "outcome must be one of"},
		{"total missing", "step_progress", progressFlagValues{done: 0}, []string{"done"}, "total must be between 1 and 500"},
		{"total above 500", "step_progress", progressFlagValues{done: 1, total: 501}, []string{"done", "total"}, "total must be between 1 and 500"},
		{"done above total", "step_progress", progressFlagValues{done: 8, total: 7}, []string{"done", "total"}, "done must be between 0 and total"},
		{"negative done", "step_progress", progressFlagValues{done: -1, total: 7}, []string{"done", "total"}, "done must be between 0 and total"},
		{"label above 120 characters", "step_progress", progressFlagValues{done: 1, total: 2, label: long}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label with a line break", "step_progress", progressFlagValues{done: 1, total: 2, label: "one\ntwo"}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label with a tab", "step_progress", progressFlagValues{done: 1, total: 2, label: "one\ttwo"}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label with an escape character", "step_progress", progressFlagValues{done: 1, total: 2, label: "one" + string(rune(0x1b)) + "[31m"}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label with a zero-width space", "step_progress", progressFlagValues{done: 1, total: 2, label: "one" + string(rune(0x200b)) + "two"}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label with a direction override", "step_progress", progressFlagValues{done: 1, total: 2, label: "one" + string(rune(0x202e)) + "two"}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label of joiners only", "step_progress", progressFlagValues{done: 1, total: 2, label: string(rune(0x200d)) + string(rune(0x200c))}, []string{"done", "total", "label"}, "label must be a single line"},
		{"label that is not UTF-8", "step_progress", progressFlagValues{done: 1, total: 2, label: "one\xff"}, []string{"done", "total", "label"}, "label must be a single line"},
		{"unknown wait subject", "wait_requested", progressFlagValues{subject: "review"}, []string{"subject"}, "subject must be one of"},
		{"outcome of another subject", "wait_resolved", progressFlagValues{subject: "question", outcome: "approved"}, []string{"subject", "outcome"}, "outcome of a question wait must be one of answered, cancelled"},
		{"round above 50", "review_round_started", progressFlagValues{round: 51}, []string{"round"}, "round must be between 1 and 50"},
		{"round missing", "review_round_ended", progressFlagValues{}, nil, "round must be between 1 and 50"},
		{"blocking above open", "review_round_ended", progressFlagValues{round: 1, findingsOpen: 1, findingsBlocking: 2}, []string{"round", "findings-open", "findings-blocking"}, "findings.blocking must not exceed the open findings"},
		{"negative finding count", "review_round_ended", progressFlagValues{round: 1, findingsFixed: -1}, []string{"round", "findings-fixed"}, "findings must hold counts between 0 and 10000"},
		{"more than 20 findings", "review_round_ended", progressFlagValues{round: 1, findingsOpen: 21, findings: slices.Repeat([]string{"low:open:One"}, 21)}, []string{"round", "findings-open", "finding"}, "items must hold at most 20 findings"},
		{"finding without a title part", "review_round_ended", progressFlagValues{round: 1, findings: []string{"low:open"}}, []string{"round", "finding"}, "--finding takes <severity>:<state>:<title>"},
		{"finding with an empty title", "review_round_ended", progressFlagValues{round: 1, findings: []string{"low:open:  "}}, []string{"round", "finding"}, "items.title must be a single line of 1 to 120 characters"},
		{"finding with an unknown severity", "review_round_ended", progressFlagValues{round: 1, findings: []string{"urgent:open:One"}}, []string{"round", "finding"}, "items.severity must be one of"},
		{"finding with an unknown state", "review_round_ended", progressFlagValues{round: 1, findings: []string{"low:wontfix:One"}}, []string{"round", "finding"}, "items.state must be one of"},
		{"finding title with a line break", "review_round_ended", progressFlagValues{round: 1, findings: []string{"low:open:One\nTwo"}}, []string{"round", "finding"}, "items.title must be a single line"},
		{"repository without an owner", "change_request_linked", progressFlagValues{repository: "zensu-cli", number: 1}, []string{"repository", "number"}, "repository must be an owner and name"},
		{"repository with a parent segment", "change_request_linked", progressFlagValues{repository: "a/../b", number: 1}, []string{"repository", "number"}, "repository must be an owner and name"},
		{"number missing", "change_request_linked", progressFlagValues{repository: "a/b"}, []string{"repository"}, "number must be a positive change request number"},
		{"unknown change request state", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, state: "draft"}, []string{"repository", "number", "state"}, "state must be one of open, merged, closed"},
		{"url over http", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "http://example.test/pull/1"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url with a query", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https://example.test/pull/1?token=x"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url with a fragment", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https://example.test/pull/1#files"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url with credentials", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https://user:secret@example.test/pull/1"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url with a space", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https://example.test/pull 1"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url with a bracket in the path", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https://example.test/pull/[1]"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url without a host", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https:///pull/1"}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"url above 500 characters", "change_request_linked", progressFlagValues{repository: "a/b", number: 1, url: "https://example.test/" + strings.Repeat("a", 500)}, []string{"repository", "number", "url"}, "url must be an https address"},
		{"head too short", "workspace_state", progressFlagValues{head: "abc123"}, []string{"head"}, "head must be 7 to 64 hexadecimal characters"},
		{"head that is not hexadecimal", "workspace_state", progressFlagValues{head: "main-branch"}, []string{"head"}, "head must be 7 to 64 hexadecimal characters"},
		{"negative file count", "workspace_state", progressFlagValues{head: "abc1234", filesChanged: -1}, []string{"head", "files-changed"}, "must be between 0 and 1000000"},
		{"unknown check", "evidence_reported", progressFlagValues{check: "e2e", outcome: "passed"}, []string{"check", "outcome"}, "check must be one of"},
		{"unknown check outcome", "evidence_reported", progressFlagValues{check: "tests", outcome: "green"}, []string{"check", "outcome"}, "outcome must be one of passed, failed, skipped"},
		{"percent above 100", "evidence_reported", progressFlagValues{check: "coverage", outcome: "passed", percent: 100.5}, []string{"check", "outcome", "percent"}, "percent must be between 0 and 100"},
		{"percent that is not a number", "evidence_reported", progressFlagValues{check: "coverage", outcome: "passed", percent: nan}, []string{"check", "outcome", "percent"}, "percent must be between 0 and 100"},
		{"negative passed count", "evidence_reported", progressFlagValues{check: "tests", outcome: "failed", passed: -1}, []string{"check", "outcome", "passed"}, "passed and failed must be between 0 and 10000000"},
		{"unknown block kind", "blocked", progressFlagValues{blockKind: "waiting"}, []string{"block-kind"}, "kind must be one of"},
		{"block reason with a line break", "blocked", progressFlagValues{blockKind: "other", reason: "one\r\ntwo"}, []string{"block-kind", "reason"}, "reason must be a single line"},
		{"unknown activity", "signal", progressFlagValues{activity: "thinking"}, []string{"activity"}, "activity must be one of"},
		{"closed by a person", "work_ended", progressFlagValues{reason: "closed_by_person"}, []string{"reason"}, "closed_by_person is recorded only by the close action in the browser"},
		{"unknown work end reason", "work_ended", progressFlagValues{reason: "done"}, []string{"reason"}, "reason must be one of finished, abandoned, handed_off"},
	} {
		set := map[string]bool{}
		for _, name := range tc.changed {
			set[name] = true
		}
		_, err := progressFactFromFlags(tc.kind, &tc.values, flagsFor(set))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestProgressCleanLine_KeepsWhatTheContractAllows(t *testing.T) {
	joined := "family " + string(rune(0x1f468)) + string(rune(0x200d)) + string(rune(0x1f469))
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"  Wire the handler  ", "Wire the handler", true},
		{strings.Repeat("ä", 120), strings.Repeat("ä", 120), true},
		{"non" + string(rune(0xa0)) + "breaking", "non" + string(rune(0xa0)) + "breaking", true},
		{joined, joined, true},
		{strings.Repeat("a", 121), "", false},
		{string(rune(0x200d)), "", false},
	} {
		got, ok := progressCleanLine(tc.in, progressMaxProseRunes)
		if ok != tc.ok || got != tc.want {
			t.Errorf("progressCleanLine(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestProgressValidators_AcceptTheContractForms(t *testing.T) {
	for _, branch := range []string{"main", "claude/work-status-cli-verbs-b3df86", "feature/a.b", "release-1.2", "user@host", "a/b/c"} {
		if !progressValidBranch(branch) {
			t.Errorf("branch %q must be accepted", branch)
		}
	}
	for _, link := range []string{"https://github.com/MKITConsulting/zensu-cli/pull/57", "https://git.example.test:8443/group/project/-/merge_requests/3", "https://example.test", "https://[2001:db8::1]/pull/1", "https://example.test/a%28b%29"} {
		if !progressValidLinkURL(link) {
			t.Errorf("url %q must be accepted", link)
		}
	}
	for _, repository := range []string{"MKITConsulting/zensu-cli", "group/sub/project", "a.b/c_d-e"} {
		if !progressValidRepository(repository) {
			t.Errorf("repository %q must be accepted", repository)
		}
	}
}

func TestReadProgressBatch_KeepsTheOrderAndRefusesTheWholeInput(t *testing.T) {
	input := "\xef\xbb\xbf" + `{"kind":"turn_started"}` + "\r\n\n" +
		`{"kind":"step_progress","payload":{"done":1,"total":3,"label":" Parse the spec "}}` + "\n" +
		`{"kind":"signal","payload":null}` + "\n" +
		`{"payload":{"activity":"editing"},"kind":"signal"}` + "\n" +
		`  {"kind":"resumed","payload":{}}  ` + "\n"
	facts, err := readProgressBatch(strings.NewReader(input))
	if err != nil {
		t.Fatalf("valid batch: %v", err)
	}
	var got []string
	for _, fact := range facts {
		got = append(got, fact.Kind+" "+string(fact.Payload))
	}
	want := []string{"turn_started ", `step_progress {"done":1,"total":3,"label":"Parse the spec"}`, "signal ", `signal {"activity":"editing"}`, "resumed "}
	if !slices.Equal(got, want) {
		t.Errorf("facts = %q, want %q", got, want)
	}

	hundred := strings.Repeat(`{"kind":"turn_started"}`+"\n", 100)
	if facts, err := readProgressBatch(strings.NewReader(hundred)); err != nil || len(facts) != 100 {
		t.Errorf("100 lines must be accepted, got %d facts and %v", len(facts), err)
	}
	for _, tc := range []struct{ name, input, want string }{
		{"no line", "\n  \n", "--batch needs at least one JSON line on stdin"},
		{"101 lines", hundred + `{"kind":"turn_started"}`, "--batch takes at most 100 facts per call"},
		{"more than 256 KiB", `{"kind":"turn_started"}` + "\n" + strings.Repeat(" ", progressMaxBatchBytes), "--batch input exceeds 256 KiB"},
		{"unknown envelope key", `{"kind":"turn_started","clientEventId":"mine-0001"}`, `--batch line 1: the line has an unknown field ("clientEventId")`},
		{"caller-supplied time", `{"kind":"turn_started","occurredAt":"2026-11-20T10:00:00Z"}`, `the line has an unknown field ("occurredAt")`},
		{"unknown payload field", `{"kind":"turn_started"}` + "\n" + `{"kind":"blocked","payload":{"kind":"other","question":"May I?"}}`, `--batch line 2: blocked: payload has an unknown field ("question")`},
		{"field on a kind without fields", `{"kind":"turn_ended","payload":{"reason":"done"}}`, `turn_ended: payload has an unknown field ("reason")`},
		{"unknown nested field", `{"kind":"review_round_ended","payload":{"round":1,"items":[{"severity":"low","state":"open","title":"One","file":"a.go"}]}}`, `payload has an unknown field ("file")`},
		{"unknown kind", `{"kind":"heartbeat"}`, `--batch line 1: unknown kind "heartbeat"`},
		{"kind missing", `{"payload":{}}`, `unknown kind ""`},
		{"payload that is no object", `{"kind":"signal","payload":["editing"]}`, "signal: payload must be an object"},
		{"wrong type", `{"kind":"step_progress","payload":{"done":"1","total":3}}`, "payload has a field of the wrong type"},
		{"not JSON", `kind=turn_started`, "--batch line 1: the line is not valid JSON"},
		{"two objects on one line", `{"kind":"turn_started"}{"kind":"turn_ended"}`, "the line is not valid JSON"},
		{"invalid value", `{"kind":"turn_started"}` + "\n\n" + `{"kind":"work_ended","payload":{"reason":"closed_by_person"}}`, "--batch line 3: work_ended: reason closed_by_person"},
	} {
		facts, err := readProgressBatch(strings.NewReader(tc.input))
		if err == nil || !strings.Contains(err.Error(), tc.want) || facts != nil {
			t.Errorf("%s: got %d facts and %v, want no fact and an error containing %q", tc.name, len(facts), err, tc.want)
		}
	}
}

func TestProgressSignalActivity_ReadsTheStoredPayload(t *testing.T) {
	if got := progressSignalActivity([]byte(`{"activity":"reviewing"}`)); got != "reviewing" {
		t.Errorf("activity = %q", got)
	}
	if got := progressSignalActivity(nil); got != "" {
		t.Errorf("activity of an empty payload = %q", got)
	}
}
