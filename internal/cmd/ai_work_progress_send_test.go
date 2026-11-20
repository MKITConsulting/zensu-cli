package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/safefile"
)

const (
	progressRoute = "POST /api/ai-work/capture/units/" + progressTestWork + "/progress"
	detailRoute   = "GET /api/ai-work/capture/units/" + progressTestWork
)

type flakyTransport struct {
	fail atomic.Bool
	next http.RoundTripper
}

func (f *flakyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.fail.Load() {
		return nil, errTransport
	}
	return f.next.RoundTrip(req)
}

type sendFixture struct {
	t      *testing.T
	api    *fakeAPI
	srv    *httptest.Server
	client *client.Client
	queue  *progressQueue
	flaky  *flakyTransport
	clock  *progressTestClock
}

func newSendFixture(t *testing.T, routes map[string]func(recordedRequest) fakeReply) *sendFixture {
	t.Helper()
	srv, api := newFakeAPI(t, routes)
	flaky := &flakyTransport{next: srv.Client().Transport}
	return &sendFixture{
		t:      t,
		api:    api,
		srv:    srv,
		flaky:  flaky,
		client: client.New(&config.Config{AccessToken: "personal-login"}, srv.URL, "", client.WithHTTPClient(&http.Client{Transport: flaky})),
		queue:  newProgressQueue(filepath.Join(t.TempDir(), "zensu-ai-work-v1"), progressTestWork, srv.URL),
		clock:  useProgressClock(t, progressTestTime),
	}
}

func (f *sendFixture) report(facts ...progressFact) (progressReport, error) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return runProgressReport(ctx, f.client, f.queue, progressTestRef, facts)
}

func (f *sendFixture) mustReport(facts ...progressFact) progressReport {
	f.t.Helper()
	report, err := f.report(facts...)
	if err != nil {
		f.t.Fatalf("report: %v", err)
	}
	return report
}

func (f *sendFixture) state() *progressState {
	f.t.Helper()
	raw, err := os.ReadFile(f.queue.statePath())
	if err != nil {
		f.t.Fatalf("state file: %v", err)
	}
	state := &progressState{}
	if err := json.Unmarshal(raw, state); err != nil {
		f.t.Fatalf("state file: %v", err)
	}
	return state
}

func (f *sendFixture) awaitPending(want int) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		state := &progressState{}
		if raw, err := os.ReadFile(f.queue.statePath()); err == nil && json.Unmarshal(raw, state) == nil && state.pending() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.t.Fatalf("the queue never held %d facts", want)
}

func (f *sendFixture) bodies() []string {
	var bodies []string
	for _, call := range f.api.calls() {
		if call.Method == "POST" {
			bodies = append(bodies, call.Raw)
		}
	}
	return bodies
}

func factKinds(t *testing.T, body string) []string {
	t.Helper()
	var decoded struct {
		Facts []progressSealedFact `json:"facts"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("request body %q: %v", body, err)
	}
	var kinds []string
	for _, fact := range decoded.Facts {
		kinds = append(kinds, fact.Kind)
	}
	return kinds
}

func sameReport(a, b progressReport) bool {
	return a.Status == b.Status && a.WorkID == b.WorkID && a.Recorded == b.Recorded && a.Queued == b.Queued && a.Reason == b.Reason && a.RetryAfterSeconds == b.RetryAfterSeconds && slices.Equal(a.Dropped, b.Dropped)
}

func recorded(n int) fakeReply {
	return fakeReply{Status: 200, Body: map[string]int{"recorded": n}}
}

func recordEverything(t *testing.T) func(recordedRequest) fakeReply {
	return func(req recordedRequest) fakeReply { return recorded(len(factKinds(t, req.Raw))) }
}

func refusal(status int, code, message string, details map[string]any) fakeReply {
	body := map[string]any{"code": code, "message": message}
	if details != nil {
		body["details"] = details
	}
	return fakeReply{Status: status, Body: body}
}

func readableDetail() func(recordedRequest) fakeReply {
	return reply(200, map[string]any{"id": progressTestWork, "ownerUserId": "9dbfeb79-a2bc-4d7a-af64-4ec9dd454490", "reportingUserId": "891419fa-f7e4-4a52-8123-2bef791876dd"})
}

func TestProgressReport_SendsOneBatchAndCountsTheReceipt(t *testing.T) {
	f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t)})
	report := f.mustReport(progressFact{Kind: "turn_started"}, progressFact{Kind: "step_progress", Payload: json.RawMessage(`{"done":1,"total":3}`)})
	if !sameReport(report, progressReport{Status: "sent", WorkID: progressTestWork, Recorded: 2}) {
		t.Fatalf("report = %+v", report)
	}
	call := f.api.only(t, "POST", "/api/ai-work/capture/units/"+progressTestWork+"/progress")
	want := `{"facts":[{"clientEventId":"86434ecd38a4ae5a96ec9f7a:turn_started:59479200","kind":"turn_started","occurredAt":"2026-11-20T10:00:00.000000Z","sessionRef":"86434ecd38a4ae5a96ec9f7a","seq":59479200},{"clientEventId":"86434ecd38a4ae5a96ec9f7a:step_progress:59479201","kind":"step_progress","occurredAt":"2026-11-20T10:00:00.000000Z","sessionRef":"86434ecd38a4ae5a96ec9f7a","seq":59479201,"payload":{"done":1,"total":3}}]}`
	if call.Raw != want {
		t.Errorf("body:\n got %s\nwant %s", call.Raw, want)
	}
	if call.Auth != "Bearer personal-login" || call.APIKey != "" {
		t.Errorf("the batch travels with the personal login only, got %q and key %q", call.Auth, call.APIKey)
	}
	if strings.Contains(call.Raw, progressTestKey) || strings.Contains(call.Raw, "scv1_") {
		t.Error("the capture key left the process")
	}
	if state := f.state(); state.pending() != 0 || state.Sessions[progressTestRef].Seq != progressTestSeq+1 {
		t.Errorf("after the receipt nothing waits and the counter stays: %+v", state)
	}

	f.clock.Advance(5 * time.Second)
	if next := f.mustReport(signalFact("editing")); next.Status != "sent" || next.Recorded != 1 {
		t.Errorf("the first signal is sent at once, got %+v", next)
	}
	f.clock.Advance(5 * time.Second)
	before := len(f.api.calls())
	if merged := f.mustReport(signalFact("testing")); !sameReport(merged, progressReport{Status: "merged", WorkID: progressTestWork}) || len(f.api.calls()) != before {
		t.Errorf("a signal inside the minute answers merged and sends nothing, got %+v", merged)
	}
	f.clock.Advance(56 * time.Second)
	if later := f.mustReport(signalFact("reviewing")); later.Status != "sent" || !strings.Contains(f.bodies()[2], `"payload":{"activity":"reviewing"}`) {
		t.Errorf("the signal after the minute carries the latest activity, got %+v and %s", later, f.bodies()[2])
	}
}

func TestProgressReport_ResendsTheStoredBatchByteForByte(t *testing.T) {
	answers := []fakeReply{{Status: 503, Body: "upstream down"}}
	var mu sync.Mutex
	f := newSendFixture(t, nil)
	f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		mu.Lock()
		defer mu.Unlock()
		if len(answers) > 0 {
			next := answers[0]
			answers = answers[1:]
			return next
		}
		return recorded(len(factKinds(t, req.Raw)))
	}}

	first := f.mustReport(progressFact{Kind: "turn_started"}, progressFact{Kind: "segment_started", Payload: json.RawMessage(`{"phase":"plan"}`)})
	if first.Status != "queued" || first.Reason != "server_error" || first.Queued != 2 || first.Recorded != 0 {
		t.Fatalf("a 503 keeps the facts and exits without an error, got %+v", first)
	}

	f.clock.Advance(2 * time.Second)
	waiting := f.mustReport(progressFact{Kind: "resumed"})
	if waiting.Status != "queued" || waiting.Reason != "backoff" || waiting.Queued != 3 || len(f.bodies()) != 1 {
		t.Fatalf("inside the back-off no request goes out, got %+v after %d requests", waiting, len(f.bodies()))
	}

	f.flaky.fail.Store(true)
	f.clock.Advance(4 * time.Second)
	offline := f.mustReport(progressFact{Kind: "turn_ended"})
	if offline.Status != "queued" || offline.Reason != "network" || offline.Queued != 4 || len(f.bodies()) != 1 {
		t.Fatalf("a network error keeps the facts, got %+v", offline)
	}

	f.flaky.fail.Store(false)
	f.clock.Advance(11 * time.Second)
	done := f.mustReport(progressFact{Kind: "session_ended", Payload: json.RawMessage(`{"reason":"ended"}`)})
	if done.Status != "sent" || done.Recorded != 5 || done.Queued != 0 {
		t.Fatalf("the next call delivers everything, got %+v", done)
	}
	bodies := f.bodies()
	if len(bodies) != 3 {
		t.Fatalf("requests = %d, want the failed batch, its repeat and the later facts", len(bodies))
	}
	if bodies[1] != bodies[0] {
		t.Errorf("the stored batch must be sent again byte for byte:\nfirst  %s\nrepeat %s", bodies[0], bodies[1])
	}
	if got := factKinds(t, bodies[2]); !slices.Equal(got, []string{"resumed", "turn_ended", "session_ended"}) {
		t.Errorf("facts queued during the outage follow in their order, got %v", got)
	}
	if state := f.state(); state.Failures != 0 || state.NotBefore != nil {
		t.Errorf("a receipt clears the back-off: %+v", state)
	}
}

func TestProgressReport_WaitsForTheRateLimit(t *testing.T) {
	limited := true
	var mu sync.Mutex
	header := map[string]string{"Retry-After": "120"}
	f := newSendFixture(t, nil)
	f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		mu.Lock()
		defer mu.Unlock()
		if limited {
			out := refusal(429, "ai_work_progress_rate_limited", "progress reports for one piece of work are limited to 600 facts per hour", nil)
			out.Header = header
			return out
		}
		return recorded(len(factKinds(t, req.Raw)))
	}}
	first := f.mustReport(progressFact{Kind: "turn_started"})
	if !sameReport(first, progressReport{Status: "queued", WorkID: progressTestWork, Queued: 1, Reason: "rate_limited", RetryAfterSeconds: 120}) {
		t.Fatalf("a 429 names the wait, got %+v", first)
	}
	f.clock.Advance(time.Minute)
	second := f.mustReport(progressFact{Kind: "turn_ended"})
	if second.Status != "queued" || second.Reason != "rate_limited" || second.RetryAfterSeconds != 60 || second.Queued != 2 || len(f.bodies()) != 1 {
		t.Fatalf("no request goes out before the server's time, got %+v after %d requests", second, len(f.bodies()))
	}
	mu.Lock()
	limited = false
	mu.Unlock()
	f.clock.Advance(time.Minute)
	third := f.mustReport(progressFact{Kind: "resumed"})
	if third.Status != "sent" || third.Recorded != 3 || len(f.bodies()) != 3 || f.bodies()[1] != f.bodies()[0] {
		t.Fatalf("after the wait the kept batch goes first, unchanged, got %+v", third)
	}

	mu.Lock()
	limited, header = true, nil
	mu.Unlock()
	if plain := f.mustReport(progressFact{Kind: "turn_started"}); plain.RetryAfterSeconds != 60 {
		t.Errorf("a 429 without the header waits a minute, got %+v", plain)
	}
	for value, want := range map[string]time.Duration{"1": time.Second, "3600": time.Hour, "99999": time.Hour, "9223372037": time.Hour, "99999999999999999999": time.Hour, "0": time.Minute, "-5": time.Minute, "-99999999999999999999": time.Minute, "soon": time.Minute, "": time.Minute} {
		if got := progressRetryAfter(value); got != want {
			t.Errorf("Retry-After %q = %v, want %v", value, got, want)
		}
	}
}

func TestProgressReport_ProvesAMissingRouteWithTheDetailRead(t *testing.T) {
	for _, status := range []int{404, 405} {
		supported := false
		var mu sync.Mutex
		f := newSendFixture(t, nil)
		f.api.routes = map[string]func(recordedRequest) fakeReply{
			progressRoute: func(req recordedRequest) fakeReply {
				mu.Lock()
				defer mu.Unlock()
				if supported {
					return recorded(len(factKinds(t, req.Raw)))
				}
				return fakeReply{Status: status, Body: "404 page not found\n"}
			},
			detailRoute: readableDetail(),
		}
		first := f.mustReport(progressFact{Kind: "turn_started"}, signalFact("editing"))
		if !sameReport(first, progressReport{Status: "unsupported", WorkID: progressTestWork, Reason: "route_missing"}) {
			t.Fatalf("status %d with a readable detail means the route is missing, got %+v", status, first)
		}
		if state := f.state(); state.pending() != 0 || state.openSignal(progressTestRef) || state.UnsupportedAt == nil {
			t.Fatalf("nothing is kept for a server without the route: %+v", state)
		}
		if got := f.api.summary(); len(got) != 2 {
			t.Fatalf("the verdict costs one report and one detail read, got %v", got)
		}

		f.clock.Advance(9*time.Minute + 59*time.Second)
		again := f.mustReport(progressFact{Kind: "turn_ended"})
		if again.Status != "unsupported" || len(f.api.calls()) != 2 || f.state().pending() != 0 {
			t.Fatalf("for ten minutes no request goes out and nothing is kept, got %+v after %d requests", again, len(f.api.calls()))
		}

		f.clock.Advance(time.Second)
		probe := f.mustReport(progressFact{Kind: "resumed"})
		if probe.Status != "unsupported" || len(f.api.calls()) != 4 {
			t.Fatalf("after ten minutes one call asks again, got %+v after %d requests", probe, len(f.api.calls()))
		}

		mu.Lock()
		supported = true
		mu.Unlock()
		f.clock.Advance(10 * time.Minute)
		upgraded := f.mustReport(progressFact{Kind: "turn_started"})
		if upgraded.Status != "sent" || upgraded.Recorded != 1 || f.state().UnsupportedAt != nil {
			t.Fatalf("an upgraded server takes the next fact and the verdict is gone, got %+v", upgraded)
		}
		if got := factKinds(t, f.bodies()[len(f.bodies())-1]); !slices.Equal(got, []string{"turn_started"}) {
			t.Errorf("facts reported to the old server are never delivered late, got %v", got)
		}
	}
}

func TestProgressReport_DoesNotTrustTheBodyOfA404(t *testing.T) {
	f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{
		progressRoute: func(recordedRequest) fakeReply { return refusal(404, "not_found", "work not available", nil) },
		detailRoute:   readableDetail(),
	})
	if report := f.mustReport(progressFact{Kind: "turn_started"}); report.Status != "unsupported" {
		t.Errorf("a readable unit decides, whatever the 404 says, got %+v", report)
	}

	for name, detail := range map[string]fakeReply{
		"a server error":           {Status: 500, Body: map[string]string{"code": "internal_error", "message": "failed to process work result"}},
		"a page that is no unit":   {Status: 200, Body: "<html>sign in</html>"},
		"another unit's detail":    {Status: 200, Body: map[string]string{"id": "891419fa-f7e4-4a52-8123-2bef791876dd"}},
		"a refusal of the caller":  {Status: 403, Body: map[string]string{"code": "forbidden", "message": "work capture requires a personal first-party CLI OAuth login"}},
		"an answer without a body": {Status: 204},
		"a 404 without a code":     {Status: 404, Body: "404 page not found\n"},
	} {
		g := newSendFixture(t, map[string]func(recordedRequest) fakeReply{
			progressRoute: func(recordedRequest) fakeReply { return fakeReply{Status: 404, Body: "404 page not found\n"} },
			detailRoute:   func(recordedRequest) fakeReply { return detail },
		})
		report := g.mustReport(progressFact{Kind: "turn_started"})
		if report.Status != "queued" || report.Reason != "server_error" || report.Queued != 1 || g.state().UnsupportedAt != nil {
			t.Errorf("%s decides nothing and keeps the fact, got %+v", name, report)
		}
	}

	h := newSendFixture(t, map[string]func(recordedRequest) fakeReply{
		progressRoute: func(recordedRequest) fakeReply { return fakeReply{Status: 404, Body: "404 page not found\n"} },
		detailRoute:   reply(401, map[string]string{"code": "unauthorized", "message": "missing user"}),
	})
	if _, err := h.report(progressFact{Kind: "turn_started"}); err == nil || !strings.Contains(err.Error(), "status 401") || h.state().pending() != 1 {
		t.Errorf("a login the server refuses is an error and keeps the fact, got %v", err)
	}
}

func TestProgressReport_DiscardsTheQueueWhenTheUnitRefuses(t *testing.T) {
	notFound := func(recordedRequest) fakeReply { return refusal(404, "not_found", "work not available", nil) }
	f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: notFound, detailRoute: notFound})
	_, err := f.report(progressFact{Kind: "turn_started"}, signalFact("editing"))
	api, ok := apiErrorOf(err)
	if !ok || api.status != 404 || api.code != "not_found" || !strings.Contains(err.Error(), "work not available") {
		t.Fatalf("a 404 confirmed by the detail read is the server's error, got %v", err)
	}
	if state := f.state(); state.pending() != 0 || state.openSignal(progressTestRef) || state.UnsupportedAt != nil || state.Full {
		t.Errorf("the unit's queue is discarded and no verdict is stored: %+v", state)
	}

	plain := newSendFixture(t, map[string]func(recordedRequest) fakeReply{
		progressRoute: func(recordedRequest) fakeReply { return fakeReply{Status: 404, Body: "404 page not found\n"} },
		detailRoute:   notFound,
	})
	if _, err := plain.report(progressFact{Kind: "turn_started"}); err == nil || !strings.Contains(err.Error(), "work not available (status 404, code not_found)") {
		t.Errorf("the error names the answer of the detail read, got %v", err)
	}

	forbidden := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply {
		return refusal(403, "forbidden", "only the current owner may report progress on this work", nil)
	}})
	if _, err := forbidden.report(progressFact{Kind: "turn_started"}); err == nil || !strings.Contains(err.Error(), "only the current owner may report progress") || forbidden.state().pending() != 0 {
		t.Errorf("a 403 is an error and discards the queue, got %v", err)
	}
	if _, err := forbidden.report(progressFact{Kind: "turn_ended"}); err == nil || len(forbidden.api.calls()) != 2 {
		t.Errorf("a 403 is not remembered: the next call asks the server again, got %v after %d requests", err, len(forbidden.api.calls()))
	}

	full := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply {
		return refusal(409, "ai_work_progress_unit_full", "this piece of work holds the most progress facts one piece of work takes; start a new one", nil)
	}})
	if _, err := full.report(progressFact{Kind: "turn_started"}); err == nil || !strings.Contains(err.Error(), "code ai_work_progress_unit_full") {
		t.Fatalf("a full unit is the server's error, got %v", err)
	}
	if state := full.state(); !state.Full || state.pending() != 0 {
		t.Errorf("a full unit is remembered and its queue discarded: %+v", state)
	}
	_, err = full.report(progressFact{Kind: "turn_ended"})
	var coded *codedError
	if !errors.As(err, &coded) || coded.code != "ai_work_progress_unit_full" || len(full.api.calls()) != 1 || full.state().pending() != 0 {
		t.Errorf("after unit_full later calls send nothing, got %v after %d requests", err, len(full.api.calls()))
	}

	gone := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: reply(410, map[string]string{"code": "gone", "message": "retired"})})
	if _, err := gone.report(progressFact{Kind: "turn_started"}); err == nil || gone.state().pending() != 0 {
		t.Errorf("an answer no rule names refuses the unit instead of looping, got %v", err)
	}

	unauthorized := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: reply(401, map[string]string{"code": "unauthorized", "message": "missing user"})})
	if _, err := unauthorized.report(progressFact{Kind: "turn_started"}); err == nil || unauthorized.state().pending() != 1 || unauthorized.state().NotBefore != nil {
		t.Errorf("a 401 is an error that keeps the facts for the next sign-in, got %v", err)
	}
}

func TestProgressReport_RemovesARefusedFactAndSendsTheRest(t *testing.T) {
	facts := []progressFact{{Kind: "turn_started"}, {Kind: "resumed"}, {Kind: "turn_ended"}}
	for _, tc := range []struct {
		name    string
		refuse  fakeReply
		dropped progressDropped
	}{
		{"an unknown kind", refusal(400, "unknown_progress_fact_kind", "fact 1: kind is not a known fact kind", map[string]any{"field": "kind", "index": 1}), progressDropped{Kind: "resumed", Code: "unknown_progress_fact_kind", Field: "kind"}},
		{"an unknown field", refusal(400, "unknown_progress_fact_field", "fact 1: payload has an unknown field", map[string]any{"field": "payload", "index": 1}), progressDropped{Kind: "resumed", Code: "unknown_progress_fact_field", Field: "payload"}},
		{"an invalid fact", refusal(400, "invalid_progress_fact", "fact 1: payload.label must be a single line of at most 120 characters", map[string]any{"field": "payload.label", "index": 1}), progressDropped{Kind: "resumed", Code: "invalid_progress_fact", Field: "payload.label"}},
		{"a reused id", refusal(409, "ai_work_progress_event_conflict", "fact 1: clientEventId is already stored with other contents", map[string]any{"field": "clientEventId", "index": 1}), progressDropped{Kind: "resumed", Code: "ai_work_progress_event_conflict", Field: "clientEventId"}},
		{"a field that is no path", refusal(400, "invalid_progress_fact", "fact 1: payload is not valid", map[string]any{"field": "<b>payload</b>\n", "index": 1}), progressDropped{Kind: "resumed", Code: "invalid_progress_fact"}},
	} {
		f := newSendFixture(t, nil)
		f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
			if slices.Contains(factKinds(t, req.Raw), "resumed") {
				return tc.refuse
			}
			return recorded(len(factKinds(t, req.Raw)))
		}}
		report := f.mustReport(facts...)
		if report.Status != "sent" || report.Recorded != 2 || !slices.Equal(report.Dropped, []progressDropped{tc.dropped}) {
			t.Errorf("%s: the named fact goes and the rest is recorded in the same call, got %+v", tc.name, report)
		}
		bodies := f.bodies()
		if len(bodies) != 2 || !slices.Equal(factKinds(t, bodies[1]), []string{"turn_started", "turn_ended"}) {
			t.Fatalf("%s: requests %v", tc.name, bodies)
		}
		var first, second struct {
			Facts []json.RawMessage `json:"facts"`
		}
		_ = json.Unmarshal([]byte(bodies[0]), &first)
		_ = json.Unmarshal([]byte(bodies[1]), &second)
		if string(second.Facts[0]) != string(first.Facts[0]) || string(second.Facts[1]) != string(first.Facts[2]) {
			t.Errorf("%s: the remaining facts must be sent unchanged", tc.name)
		}
		state := f.state()
		if len(state.Rejected) != 1 || state.Rejected[0].ClientEventID != progressEventID(progressTestRef, "resumed", progressTestSeq+1) || state.Rejected[0].Code != tc.dropped.Code {
			t.Errorf("%s: the refused fact is logged in the state file: %+v", tc.name, state.Rejected)
		}
	}
}

func TestProgressReport_SplitsAndBisectsARefusedBatch(t *testing.T) {
	facts := []progressFact{{Kind: "turn_started"}, {Kind: "segment_started", Payload: json.RawMessage(`{"phase":"plan"}`)}, {Kind: "resumed"}, {Kind: "turn_ended"}}

	tooLarge := newSendFixture(t, nil)
	tooLarge.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		if len(factKinds(t, req.Raw)) > 2 {
			return refusal(413, "request_too_large", "batch is larger than 256 KiB", map[string]any{"field": "batch"})
		}
		return recorded(len(factKinds(t, req.Raw)))
	}}
	report := tooLarge.mustReport(facts...)
	if report.Status != "sent" || report.Recorded != 4 || len(report.Dropped) != 0 {
		t.Fatalf("a batch the server finds too large is split, got %+v", report)
	}
	var sent [][]string
	for _, body := range tooLarge.bodies() {
		sent = append(sent, factKinds(t, body))
	}
	if len(sent) != 3 || !slices.Equal(sent[1], []string{"turn_started", "segment_started"}) || !slices.Equal(sent[2], []string{"resumed", "turn_ended"}) {
		t.Errorf("the halves keep the order, got %v", sent)
	}

	tooMany := newSendFixture(t, nil)
	tooMany.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply {
		return refusal(400, "progress_batch_too_large", "facts must hold at most 100 facts", map[string]any{"field": "facts"})
	}}
	alone := tooMany.mustReport(progressFact{Kind: "turn_started"})
	if alone.Status != "sent" || !slices.Equal(alone.Dropped, []progressDropped{{Kind: "turn_started", Code: "progress_batch_too_large", Field: "facts"}}) {
		t.Errorf("a single fact that is still refused as too large is dropped, got %+v", alone)
	}

	proxied := newSendFixture(t, nil)
	proxied.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		if len(factKinds(t, req.Raw)) > 2 {
			return fakeReply{Status: 413, Body: "Payload Too Large"}
		}
		return recorded(len(factKinds(t, req.Raw)))
	}}
	if report := proxied.mustReport(facts...); report.Status != "sent" || report.Recorded != 4 || len(report.Dropped) != 0 {
		t.Errorf("a batch too large for something in front of the API is split as well, got %+v", report)
	}

	for name, unindexed := range map[string]fakeReply{
		"a refusal of the batch's form": refusal(400, "invalid_progress_fact", "batch is not valid JSON", map[string]any{"field": "batch"}),
		"an index outside the batch":    refusal(400, "invalid_progress_fact", "fact 9: kind is not a known fact kind", map[string]any{"field": "kind", "index": 9}),
	} {
		f := newSendFixture(t, nil)
		f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
			if slices.Contains(factKinds(t, req.Raw), "resumed") {
				return unindexed
			}
			return recorded(len(factKinds(t, req.Raw)))
		}}
		report := f.mustReport(facts...)
		if report.Status != "sent" || report.Recorded != 3 || len(report.Dropped) != 1 || report.Dropped[0].Kind != "resumed" {
			t.Errorf("%s: the batch is bisected until the refused fact stands alone, got %+v", name, report)
		}
		if got := f.state().pending(); got != 0 {
			t.Errorf("%s: %d facts are left", name, got)
		}
	}
}

func TestProgressReport_KeepsTheBatchOnTransientAnswers(t *testing.T) {
	for name, answer := range map[string]fakeReply{
		"a plain conflict":             refusal(409, "conflict", "progress facts changed while they were recorded; send the batch again", nil),
		"a busy resource":              refusal(503, "resource_busy", "the resource is busy", nil),
		"an internal error":            refusal(500, "internal_error", "failed to record work progress", nil),
		"a gateway timeout":            {Status: 504, Body: ""},
		"a request timeout":            {Status: 408, Body: ""},
		"an answer that is no receipt": {Status: 200, Body: "<html>captive portal</html>"},
		"a receipt without a count":    {Status: 200, Body: map[string]string{"status": "ok"}},
		"a refusal without a code":     {Status: 400, Body: "Bad Request"},
		"a conflict without a code":    {Status: 409, Body: ""},
		"a block page before the API":  {Status: 403, Body: "<html>blocked</html>"},
		"one fact a proxy finds large": {Status: 413, Body: "Payload Too Large"},
	} {
		f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply { return answer }})
		report := f.mustReport(progressFact{Kind: "turn_started"})
		if report.Status != "queued" || report.Reason != "server_error" || report.Queued != 1 || len(report.Dropped) != 0 {
			t.Errorf("%s keeps the batch, got %+v", name, report)
		}
		if state := f.state(); len(state.Batch) != 1 || state.Failures != 1 || state.WaitReason != "backoff" {
			t.Errorf("%s: the batch stays in flight with a back-off: %+v", name, state)
		}
	}
}

func TestProgressReport_SerializesParallelCallsForOneUnit(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	var mu sync.Mutex
	var arrived []progressSealedFact
	f := newSendFixture(t, nil)
	f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		now := inFlight.Add(1)
		for {
			seen := maxInFlight.Load()
			if now <= seen || maxInFlight.CompareAndSwap(seen, now) {
				break
			}
		}
		time.Sleep(3 * time.Millisecond)
		var decoded struct {
			Facts []progressSealedFact `json:"facts"`
		}
		_ = json.Unmarshal([]byte(req.Raw), &decoded)
		mu.Lock()
		arrived = append(arrived, decoded.Facts...)
		mu.Unlock()
		inFlight.Add(-1)
		return recorded(len(decoded.Facts))
	}}
	const callers, each = 10, 3
	var group sync.WaitGroup
	for caller := 0; caller < callers; caller++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < each; i++ {
				report, err := f.report(progressFact{Kind: "turn_started"})
				if err != nil || (report.Status != "sent" && report.Status != "queued") {
					t.Errorf("parallel report: %+v, %v", report, err)
				}
			}
		}()
	}
	group.Wait()
	if last := f.mustReport(progressFact{Kind: "turn_ended"}); last.Status != "sent" {
		t.Fatalf("the last call leaves nothing behind, got %+v", last)
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("the server saw %d overlapping requests for one unit, want 1", got)
	}
	ids := map[string]bool{}
	var seqs []int64
	for _, fact := range arrived {
		ids[fact.ClientEventID] = true
		seqs = append(seqs, fact.Seq)
	}
	if len(arrived) != callers*each+1 || len(ids) != len(arrived) {
		t.Errorf("%d facts arrived with %d distinct ids, want %d of each", len(arrived), len(ids), callers*each+1)
	}
	if !slices.IsSorted(seqs) {
		t.Errorf("facts must arrive in the order of their sequence, got %v", seqs)
	}
}

func TestProgressReport_ReturnsInsideItsTimeBudget(t *testing.T) {
	release := make(chan struct{})
	f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply {
		<-release
		return recorded(1)
	}})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	report, err := runProgressReport(ctx, f.client, f.queue, progressTestRef, []progressFact{{Kind: "turn_started"}})
	if elapsed := time.Since(start); err != nil || report.Status != "queued" || report.Reason != "timeout" || report.Queued != 1 || elapsed > 3*time.Second {
		t.Fatalf("a server that never answers costs the budget and keeps the fact, got %+v, %v after %v", report, err, elapsed)
	}
	if state := f.state(); len(state.Batch) != 1 || state.NotBefore == nil {
		t.Errorf("the batch stays in flight for the next call: %+v", state)
	}

	busy := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t)})
	if err := busy.queue.prepare(); err != nil {
		t.Fatal(err)
	}
	unlock, err := safefile.Lock(context.Background(), busy.queue.sendLockPath())
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer stop()
	held, err := runProgressReport(short, busy.client, busy.queue, progressTestRef, []progressFact{{Kind: "turn_started"}})
	if err != nil || held.Status != "queued" || held.Reason != "busy" || held.Queued != 1 || len(busy.api.calls()) != 0 {
		t.Fatalf("a call that cannot get the send lock leaves its fact for the holder, got %+v, %v", held, err)
	}
	unlock()
	if after := busy.mustReport(progressFact{Kind: "turn_ended"}); after.Status != "sent" || after.Recorded != 2 {
		t.Errorf("the next call delivers the fact that waited, got %+v", after)
	}
}

func TestProgressRefusalDetails_KeepsOnlyAFieldPath(t *testing.T) {
	longest := strings.Repeat("a", 64)
	for raw, want := range map[string]struct {
		index int
		field string
	}{
		`{"details":{"index":1,"field":"payload.findings[3].title"}}`: {1, "payload.findings[3].title"},
		`{"details":{"index":0,"field":"` + longest + `"}}`:           {0, longest},
		`{"details":{"index":0,"field":"` + longest + `a"}}`:          {0, ""},
		`{"details":{"index":2,"field":"<script>"}}`:                  {2, ""},
		`{"details":{"index":2,"field":"kind\u001b[2J"}}`:             {2, ""},
		`{"details":{"index":2,"field":"two words"}}`:                 {2, ""},
		`{"details":{"field":"facts"}}`:                               {-1, "facts"},
		`{"details":{"index":"1","field":"kind"}}`:                    {-1, "kind"},
		`{"code":"invalid_progress_fact"}`:                            {-1, ""},
		`Bad Request`:                                                 {-1, ""},
	} {
		if index, field := progressRefusalDetails([]byte(raw)); index != want.index || field != want.field {
			t.Errorf("%s: index %d field %q, want %d %q", raw, index, field, want.index, want.field)
		}
	}
}

func TestProgressReport_ResendsABatchWhoseReceiptBrokeOff(t *testing.T) {
	cut := true
	var mu sync.Mutex
	f := newSendFixture(t, nil)
	f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		mu.Lock()
		defer mu.Unlock()
		if cut {
			return fakeReply{Status: 200, Body: `{"recor`, Header: map[string]string{"Content-Length": "4096"}}
		}
		return recorded(len(factKinds(t, req.Raw)))
	}}
	first, err := f.report(progressFact{Kind: "turn_started"})
	if err != nil || first.Status != "queued" || first.Reason != "network" || first.Queued != 1 || first.Recorded != 0 {
		t.Fatalf("a receipt that breaks off decides nothing and is no error, got %+v, %v", first, err)
	}
	if state := f.state(); len(state.Batch) != 1 || state.NotBefore == nil || state.WaitReason != "backoff" {
		t.Errorf("the batch stays in flight with a back-off: %+v", state)
	}

	mu.Lock()
	cut = false
	mu.Unlock()
	f.clock.Advance(6 * time.Second)
	second := f.mustReport(progressFact{Kind: "turn_ended"})
	bodies := f.bodies()
	if second.Status != "sent" || second.Recorded != 2 || len(bodies) != 3 || bodies[1] != bodies[0] {
		t.Fatalf("the next call repeats the batch unchanged and then sends the rest, got %+v after %d requests", second, len(bodies))
	}
}

func TestProgressReport_KeepsTheFactsWhenTheLoginCannotBeRenewed(t *testing.T) {
	const progressPath = "/api/ai-work/capture/units/" + progressTestWork + "/progress"
	token := fakeReply{Status: 400, Body: map[string]string{"error": "invalid_grant", "error_description": "the refresh token was revoked"}}
	var mu sync.Mutex
	f := newSendFixture(t, nil)
	f.api.routes = map[string]func(recordedRequest) fakeReply{
		progressRoute: recordEverything(t),
		"POST /oauth/token": func(recordedRequest) fakeReply {
			mu.Lock()
			defer mu.Unlock()
			return token
		},
	}
	f.client = client.New(&config.Config{AccessToken: "expired", RefreshToken: "r1", ExpiresAt: time.Now().Add(-time.Hour)}, f.srv.URL, f.srv.URL+"/oauth/token",
		client.WithHTTPClient(&http.Client{Transport: f.flaky}),
		client.WithSaver(func(*config.Config) error { return nil }),
	)

	_, err := f.report(progressFact{Kind: "turn_started"})
	if err == nil || !strings.Contains(err.Error(), "refreshing session: token endpoint: invalid_grant") {
		t.Fatalf("a login the token endpoint refuses is an error, got %v", err)
	}
	if state := f.state(); len(state.Batch) != 1 || state.NotBefore != nil || f.api.count("POST", progressPath) != 0 {
		t.Errorf("the fact stays in flight without a back-off and nothing reaches the progress route: %+v", state)
	}

	f.flaky.fail.Store(true)
	offline := f.mustReport(progressFact{Kind: "turn_ended"})
	if offline.Status != "queued" || offline.Reason != "network" || offline.Queued != 2 {
		t.Fatalf("a token endpoint that cannot be reached keeps the facts and is no error, got %+v", offline)
	}

	f.flaky.fail.Store(false)
	mu.Lock()
	token = fakeReply{Status: 200, Body: map[string]any{"access_token": "renewed", "expires_in": 3600}}
	mu.Unlock()
	f.clock.Advance(6 * time.Second)
	done := f.mustReport(progressFact{Kind: "resumed"})
	if done.Status != "sent" || done.Recorded != 3 || done.Queued != 0 {
		t.Fatalf("after the login is renewed the kept facts go out, got %+v", done)
	}
	for _, call := range f.api.calls() {
		if call.Path == progressPath && call.Auth != "Bearer renewed" {
			t.Errorf("a batch travelled with %q, want the renewed login", call.Auth)
		}
	}
}

func TestProgressReport_ObeysWhatAParallelCallLearnedWhileItWaited(t *testing.T) {
	for _, tc := range []struct {
		name    string
		learned func(*progressState, time.Time)
		want    progressReport
		failure string
	}{
		{"a rate limit", func(state *progressState, now time.Time) { state.wait(now, progressWaitRateLimited, 2*time.Minute) }, progressReport{Status: "queued", WorkID: progressTestWork, Queued: 1, Reason: "rate_limited", RetryAfterSeconds: 120}, ""},
		{"a back-off", func(state *progressState, now time.Time) { state.wait(now, progressWaitBackoff, 30*time.Second) }, progressReport{Status: "queued", WorkID: progressTestWork, Queued: 1, Reason: "backoff"}, ""},
		{"a missing route", func(state *progressState, now time.Time) { state.discard(); state.UnsupportedAt = &now }, progressReport{Status: "unsupported", WorkID: progressTestWork, Reason: "route_missing"}, ""},
		{"a full unit", func(state *progressState, _ time.Time) { state.discard(); state.Full = true }, progressReport{}, "takes no more progress facts"},
		{"a refusal of the work", func(state *progressState, _ time.Time) { state.discard() }, progressReport{}, "discarded by a parallel report"},
	} {
		f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{})
		if err := f.queue.prepare(); err != nil {
			t.Fatal(err)
		}
		unlock, err := safefile.Lock(context.Background(), f.queue.sendLockPath())
		if err != nil {
			t.Fatal(err)
		}
		type outcome struct {
			report progressReport
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			report, err := runProgressReport(ctx, f.client, f.queue, progressTestRef, []progressFact{{Kind: "turn_started"}})
			done <- outcome{report, err}
		}()
		f.awaitPending(1)
		if err := f.queue.update(context.Background(), func(state *progressState, now time.Time) error {
			tc.learned(state, now)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		unlock()
		got := <-done
		switch {
		case tc.failure != "" && (got.err == nil || !strings.Contains(got.err.Error(), tc.failure)):
			t.Errorf("%s: the waiting call must fail with %q, got %+v, %v", tc.name, tc.failure, got.report, got.err)
		case tc.failure == "" && (got.err != nil || !sameReport(got.report, tc.want)):
			t.Errorf("%s: the waiting call answers %+v, %v, want %+v", tc.name, got.report, got.err, tc.want)
		}
		if calls := f.api.summary(); len(calls) != 0 {
			t.Errorf("%s: the waiting call still asked the server: %v", tc.name, calls)
		}
	}
}

func TestProgressReport_FailsWhenItsBatchVanishedWhileItWasSent(t *testing.T) {
	f := newSendFixture(t, nil)
	f.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(req recordedRequest) fakeReply {
		if err := f.queue.update(context.Background(), func(state *progressState, _ time.Time) error {
			state.Batch, state.Queue = nil, nil
			return nil
		}); err != nil {
			t.Errorf("emptying the queue: %v", err)
		}
		return recorded(len(factKinds(t, req.Raw)))
	}}
	report, err := f.report(progressFact{Kind: "turn_started"})
	if err == nil || !strings.Contains(err.Error(), "discarded by a parallel report") {
		t.Fatalf("a batch that is no longer the stored one must not be reported as sent, got %+v, %v", report, err)
	}
	if len(f.bodies()) != 1 {
		t.Errorf("the call must stop after the request whose batch vanished, sent %d", len(f.bodies()))
	}

	late := newSendFixture(t, nil)
	release := make(chan struct{})
	late.api.routes = map[string]func(recordedRequest) fakeReply{progressRoute: func(recordedRequest) fakeReply {
		if err := late.queue.update(context.Background(), func(state *progressState, _ time.Time) error {
			state.Batch, state.Queue = nil, nil
			return nil
		}); err != nil {
			t.Errorf("emptying the queue: %v", err)
		}
		<-release
		return recorded(1)
	}}
	t.Cleanup(func() { close(release) })
	short, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	report, err = runProgressReport(short, late.client, late.queue, progressTestRef, []progressFact{{Kind: "turn_started"}})
	if err == nil || !strings.Contains(err.Error(), "discarded by a parallel report") {
		t.Fatalf("a call whose budget ended must not report a vanished batch as sent either, got %+v, %v", report, err)
	}
}

func TestProgressReport_ReadsTheGateEvenWhenItsBudgetHasEnded(t *testing.T) {
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name    string
		learned func(*progressState, time.Time) error
		want    progressReport
		failure string
	}{
		{"one more fact", func(state *progressState, now time.Time) error {
			return state.enqueue(progressTestRef, plainFacts("turn_ended"), now)
		}, progressReport{Status: "queued", WorkID: progressTestWork, Queued: 2, Reason: "timeout"}, ""},
		{"a missing route", func(state *progressState, now time.Time) error {
			state.discard()
			state.UnsupportedAt = &now
			return nil
		}, progressReport{Status: "unsupported", WorkID: progressTestWork, Reason: "route_missing"}, ""},
		{"a full unit", func(state *progressState, _ time.Time) error {
			state.discard()
			state.Full = true
			return nil
		}, progressReport{}, "takes no more progress facts"},
		{"a refusal of the work", func(state *progressState, _ time.Time) error {
			state.discard()
			return nil
		}, progressReport{}, "discarded by a parallel report"},
		{"a stored wait", func(state *progressState, now time.Time) error {
			state.wait(now, progressWaitRateLimited, 2*time.Minute)
			return nil
		}, progressReport{Status: "queued", WorkID: progressTestWork, Queued: 1, Reason: "timeout"}, ""},
	} {
		for exit, leave := range map[string]func(*progressRun) (progressReport, error){
			"the send loop":       func(run *progressRun) (progressReport, error) { return run.flush(ended) },
			"the busy lock's end": func(run *progressRun) (progressReport, error) { return run.settle(progressReasonTimeout) },
		} {
			f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{})
			if err := f.queue.prepare(); err != nil {
				t.Fatal(err)
			}
			run := &progressRun{client: f.client, queue: f.queue, sessionRef: progressTestRef, report: progressReport{WorkID: progressTestWork}}
			if err := run.update(func(state *progressState, now time.Time) error {
				return state.enqueue(progressTestRef, plainFacts("turn_started"), now)
			}); err != nil {
				t.Fatal(err)
			}
			if err := f.queue.update(context.Background(), tc.learned); err != nil {
				t.Fatal(err)
			}
			before := f.state()
			report, err := leave(run)
			switch {
			case tc.failure != "" && (err == nil || !strings.Contains(err.Error(), tc.failure)):
				t.Errorf("%s at %s: a call without a budget must still fail with %q, got %+v, %v", tc.name, exit, tc.failure, report, err)
			case tc.failure == "" && (err != nil || !sameReport(report, tc.want)):
				t.Errorf("%s at %s: a call without a budget answers %+v, %v, want %+v", tc.name, exit, report, err, tc.want)
			}
			if calls := f.api.summary(); len(calls) != 0 {
				t.Errorf("%s at %s: a call without a budget still asked the server: %v", tc.name, exit, calls)
			}
			after := f.state()
			sameWait := (before.NotBefore == nil) == (after.NotBefore == nil) && (before.NotBefore == nil || before.NotBefore.Equal(*after.NotBefore))
			if len(after.Batch) != 0 || after.Failures != before.Failures || !sameWait || after.pending() != before.pending() {
				t.Errorf("%s at %s: a call without a budget must form no batch and store no wait, state before %+v, after %+v", tc.name, exit, before, after)
			}
		}
	}
}

func TestProgressReport_ReadsTheGateWhenTheSendLockStaysBusy(t *testing.T) {
	f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{})
	if err := f.queue.prepare(); err != nil {
		t.Fatal(err)
	}
	unlock, err := safefile.Lock(context.Background(), f.queue.sendLockPath())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	type outcome struct {
		report progressReport
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		report, err := runProgressReport(ctx, f.client, f.queue, progressTestRef, []progressFact{{Kind: "turn_started"}})
		done <- outcome{report, err}
	}()
	f.awaitPending(1)
	if err := f.queue.update(context.Background(), func(state *progressState, _ time.Time) error {
		state.discard()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err == nil || !strings.Contains(got.err.Error(), "discarded by a parallel report") {
		t.Fatalf("a call that never got the send lock must not count facts a parallel call discarded, got %+v, %v", got.report, got.err)
	}
	if calls := f.api.summary(); len(calls) != 0 {
		t.Errorf("a call without the send lock asked the server: %v", calls)
	}
}

func TestProgressReport_DeliversWhatAnotherWorkLeftBehind(t *testing.T) {
	const other = "5f0c8e0e-6d5c-4c59-9d0b-3a8c1e7f2b11"
	const otherPath = "/api/ai-work/capture/units/" + other + "/progress"
	const otherRoute = "POST " + otherPath
	leave := func(f *sendFixture, apiURL string, age time.Duration, change func(*progressState, time.Time)) *progressQueue {
		t.Helper()
		if err := f.queue.prepare(); err != nil {
			t.Fatal(err)
		}
		sibling := &progressQueue{dir: f.queue.dir, workID: other, apiURL: apiURL}
		if err := sibling.update(context.Background(), func(state *progressState, now time.Time) error {
			if err := state.enqueue(progressTestRef, plainFacts("turn_ended", "resumed"), now); err != nil {
				return err
			}
			if change != nil {
				change(state, now)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		at := f.clock.Now().Add(-age)
		if err := os.Chtimes(sibling.statePath(), at, at); err != nil {
			t.Fatal(err)
		}
		return sibling
	}
	stored := func(queue *progressQueue) string {
		t.Helper()
		raw, err := os.ReadFile(queue.statePath())
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	left := func(queue *progressQueue) int {
		t.Helper()
		state := &progressState{}
		if err := json.Unmarshal([]byte(stored(queue)), state); err != nil {
			t.Fatal(err)
		}
		return state.pending()
	}

	f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t), otherRoute: recordEverything(t)})
	sibling := leave(f, f.queue.apiURL, time.Hour, nil)
	report := f.mustReport(progressFact{Kind: "turn_started"})
	if !sameReport(report, progressReport{Status: "sent", WorkID: progressTestWork, Recorded: 1}) {
		t.Fatalf("the result speaks of the reported work only, got %+v", report)
	}
	if kinds := factKinds(t, f.api.only(t, "POST", otherPath).Raw); !slices.Equal(kinds, []string{"turn_ended", "resumed"}) || left(sibling) != 0 {
		t.Errorf("what the other work left behind goes out after the own facts, got %v with %d left", kinds, left(sibling))
	}

	merged := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t), otherRoute: recordEverything(t)})
	merged.mustReport(signalFact("editing"))
	sibling = leave(merged, merged.queue.apiURL, 0, nil)
	merged.clock.Advance(5 * time.Second)
	if report := merged.mustReport(signalFact("testing")); report.Status != "merged" || merged.api.count("POST", otherPath) != 1 || left(sibling) != 0 {
		t.Errorf("a merged signal delivers the other work's facts as well, got %+v with %d left", report, left(sibling))
	}

	refused := newSendFixture(t, map[string]func(recordedRequest) fakeReply{
		progressRoute: recordEverything(t),
		otherRoute: func(recordedRequest) fakeReply {
			return refusal(403, "forbidden", "only the current owner may report progress on this work", nil)
		},
	})
	sibling = leave(refused, refused.queue.apiURL, 0, nil)
	if report, err := refused.report(progressFact{Kind: "turn_started"}); err != nil || report.Status != "sent" || report.Recorded != 1 || left(sibling) != 0 {
		t.Errorf("a refusal of the other work is not the result of this one, got %+v, %v with %d left", report, err, left(sibling))
	}

	down := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: reply(503, "upstream down")})
	sibling = leave(down, down.queue.apiURL, 0, nil)
	if report := down.mustReport(progressFact{Kind: "turn_started"}); report.Status != "queued" || down.api.count("POST", otherPath) != 0 || left(sibling) != 2 {
		t.Errorf("a call that could not send its own facts leaves the other work alone, got %+v", report)
	}

	for name, prepare := range map[string]func(*sendFixture) *progressQueue{
		"another API host": func(f *sendFixture) *progressQueue { return leave(f, "https://other.test", 0, nil) },
		"a running wait": func(f *sendFixture) *progressQueue {
			return leave(f, f.queue.apiURL, 0, func(state *progressState, now time.Time) { state.wait(now, progressWaitBackoff, 30*time.Second) })
		},
		"a full unit": func(f *sendFixture) *progressQueue {
			return leave(f, f.queue.apiURL, 0, func(state *progressState, _ time.Time) { state.Full = true })
		},
		"facts older than a day": func(f *sendFixture) *progressQueue { return leave(f, f.queue.apiURL, 25*time.Hour, nil) },
		"a file that is no state": func(f *sendFixture) *progressQueue {
			sibling := leave(f, f.queue.apiURL, 0, nil)
			if err := os.WriteFile(sibling.statePath(), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			return sibling
		},
		"a state under another unit's name": func(f *sendFixture) *progressQueue {
			sibling := leave(f, f.queue.apiURL, 0, nil)
			renamed := strings.ReplaceAll(stored(sibling), other, "891419fa-f7e4-4a52-8123-2bef791876dd")
			if err := os.WriteFile(sibling.statePath(), []byte(renamed), 0o600); err != nil {
				t.Fatal(err)
			}
			return sibling
		},
		"a send in progress": func(f *sendFixture) *progressQueue {
			sibling := leave(f, f.queue.apiURL, 0, nil)
			unlock, err := safefile.Lock(context.Background(), sibling.sendLockPath())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(unlock)
			return sibling
		},
	} {
		g := newSendFixture(t, map[string]func(recordedRequest) fakeReply{progressRoute: recordEverything(t)})
		sibling := prepare(g)
		before := stored(sibling)
		if report := g.mustReport(progressFact{Kind: "turn_started"}); report.Status != "sent" || report.Recorded != 1 {
			t.Errorf("%s: the own facts go out, got %+v", name, report)
		}
		if g.api.count("POST", otherPath) != 0 || stored(sibling) != before {
			t.Errorf("%s: the other work's queue must stay as it is", name)
		}
	}
}
