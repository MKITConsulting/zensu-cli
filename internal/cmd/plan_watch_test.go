package cmd

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func stubWatchSleep(t *testing.T, result error) *[]time.Duration {
	t.Helper()
	sleeps := &[]time.Duration{}
	prev := planWatchSleep
	planWatchSleep = func(_ context.Context, d time.Duration) error {
		*sleeps = append(*sleeps, d)
		return result
	}
	t.Cleanup(func() { planWatchSleep = prev })
	return sleeps
}

func replySequence(polls *int32, replies ...fakeReply) func(recordedRequest) fakeReply {
	return func(recordedRequest) fakeReply {
		i := int(atomic.AddInt32(polls, 1)) - 1
		if i >= len(replies) {
			i = len(replies) - 1
		}
		return replies[i]
	}
}

func unavailable() fakeReply {
	return fakeReply{Status: 503, Body: `{"code":"unavailable","message":"backend down"}`}
}

func durations(ds []time.Duration) string {
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, d.String())
	}
	return strings.Join(parts, " ")
}

func watchCmd(f *Factory) (*cobra.Command, *bytes.Buffer) {
	stderr := &bytes.Buffer{}
	c := NewPlanCmd(f)
	c.SilenceErrors = true
	c.SetErr(stderr)
	return c, stderr
}

func baseURLFactory(base string, h *http.Client) *Factory {
	return &Factory{Out: &bytes.Buffer{}, NewClient: func(context.Context) (*client.Client, error) {
		return client.New(&config.Config{APIKey: "zsk_test"}, base, "", client.WithHTTPClient(h)), nil
	}}
}

func refusedFactory(t *testing.T) *Factory {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	base := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	return baseURLFactory(base, &http.Client{})
}

func TestPlanStatus_WatchRetriesTransientFailures(t *testing.T) {
	sleeps := stubWatchSleep(t, nil)
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: replySequence(&polls,
			unavailable(),
			fakeReply{Status: 429, Body: `{"code":"rate_limited","message":"slow down"}`},
			fakeReply{Body: planDetail("open", map[string]any{"id": "o1", "status": "implementing", "attempt": 1})},
			fakeReply{Body: planDetail("merged", map[string]any{"id": "o1", "status": "merged", "attempt": 1})},
		),
	})
	f, out := testFactory(srv)
	c, stderr := watchCmd(f)
	if err := runCmd(t, c, "status", plPlan, "--watch", "--interval", "1s"); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if polls != 4 || durations(*sleeps) != "1s 2s 1s" {
		t.Fatalf("polls %d, sleeps %q, want 4 polls and 1s 2s 1s", polls, durations(*sleeps))
	}
	if strings.Count(out.String(), "Plan "+plPlan) != 2 || !strings.Contains(out.String(), `"Checkout": open`) || !strings.Contains(out.String(), `"Checkout": merged`) {
		t.Fatalf("output:\n%s", out.String())
	}
	want := "poll 1 of 5 failed: backend down (status 503); retrying in 1s\npoll 2 of 5 failed: slow down (status 429); retrying in 2s\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestPlanStatus_WatchGivesUpAfterFiveFailedPollsInARow(t *testing.T) {
	sleeps := stubWatchSleep(t, nil)
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: replySequence(&polls,
			fakeReply{Body: planDetail("open")},
			unavailable(), unavailable(), unavailable(), unavailable(), unavailable(),
		),
	})
	f, out := testFactory(srv)
	c, stderr := watchCmd(f)
	err := runCmd(t, c, "status", plPlan, "--watch", "--interval", "1s")
	if err == nil || err.Error() != "stopped watching after 5 failed polls in a row: backend down (status 503)" {
		t.Fatalf("error = %v", err)
	}
	if polls != 6 || durations(*sleeps) != "1s 1s 2s 4s 8s" {
		t.Fatalf("polls %d, sleeps %q", polls, durations(*sleeps))
	}
	if strings.Count(out.String(), "Plan "+plPlan) != 1 {
		t.Fatalf("output:\n%s", out.String())
	}
	want := "poll 1 of 5 failed: backend down (status 503); retrying in 1s\n" +
		"poll 2 of 5 failed: backend down (status 503); retrying in 2s\n" +
		"poll 3 of 5 failed: backend down (status 503); retrying in 4s\n" +
		"poll 4 of 5 failed: backend down (status 503); retrying in 8s\n"
	if stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func TestPlanStatus_WatchRetriesARefusedConnection(t *testing.T) {
	sleeps := stubWatchSleep(t, nil)
	c, stderr := watchCmd(refusedFactory(t))
	err := runCmd(t, c, "status", plPlan, "--watch", "--interval", "2s")
	if err == nil || !strings.HasPrefix(err.Error(), "stopped watching after 5 failed polls in a row: ") {
		t.Fatalf("error = %v", err)
	}
	if durations(*sleeps) != "2s 4s 8s 16s" {
		t.Fatalf("sleeps %q", durations(*sleeps))
	}
	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")
	waits := []string{"2s", "4s", "8s", "16s"}
	if len(lines) != len(waits) {
		t.Fatalf("stderr = %q, want one line per retried poll", stderr.String())
	}
	for i, wait := range waits {
		if !strings.HasPrefix(lines[i], fmt.Sprintf("poll %d of 5 failed: ", i+1)) || !strings.HasSuffix(lines[i], "; retrying in "+wait) {
			t.Fatalf("stderr line %d = %q", i+1, lines[i])
		}
	}
}

func TestPlanStatus_WatchRetriesAResponseCutOffMidBody(t *testing.T) {
	sleeps := stubWatchSleep(t, nil)
	var polls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if atomic.AddInt32(&polls, 1) == 1 {
			w.Header().Set("Content-Length", "4096")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"id":"`+plPlan+`","status":`)
			return
		}
		_ = json.NewEncoder(w).Encode(planDetail("merged"))
	}))
	defer srv.Close()
	f, out := testFactory(srv)
	c, stderr := watchCmd(f)
	if err := runCmd(t, c, "status", plPlan, "--watch", "--interval", "1s"); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if polls != 2 || durations(*sleeps) != "1s" {
		t.Fatalf("polls %d, sleeps %q, want the cut-off poll retried once", polls, durations(*sleeps))
	}
	if stderr.String() != "poll 1 of 5 failed: reading response body: unexpected EOF; retrying in 1s\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(out.String(), `"Checkout": merged`) {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestPlanStatus_WatchStopsAtOnceOnPermanentErrors(t *testing.T) {
	var redirects int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&redirects, 1)
		http.Redirect(w, r, "http://evil.test"+r.URL.Path, http.StatusFound)
	}))
	defer redirect.Close()
	untrusted := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a server whose certificate failed verification must not receive the poll")
	}))
	untrusted.Config.ErrorLog = log.New(io.Discard, "", 0)
	untrusted.StartTLS()
	defer untrusted.Close()
	failing, _ := failingFactory()
	cases := []struct {
		name string
		f    *Factory
		want string
	}{
		{"refused cross-host redirect", baseURLFactory(redirect.URL, redirect.Client()), "refusing cross-host redirect to evil.test"},
		{"certificate of an unknown authority", baseURLFactory(untrusted.URL, &http.Client{}), "certificate"},
		{"invalid API URL", baseURLFactory("http://[::1", &http.Client{}), "creating request: "},
		{"unsupported protocol scheme", baseURLFactory("ftp://zensu.test", &http.Client{}), `unsupported protocol scheme "ftp"`},
		{"unclassified transport error", failing, "dial refused by test transport"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sleeps := stubWatchSleep(t, nil)
			c, stderr := watchCmd(tc.f)
			err := runCmd(t, c, "status", plPlan, "--watch", "--interval", "1s")
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.HasPrefix(err.Error(), "stopped watching") {
				t.Fatalf("error = %v, want the first poll to end the watch with %q", err, tc.want)
			}
			if len(*sleeps) != 0 || stderr.Len() != 0 {
				t.Fatalf("sleeps %q, stderr %q, want no retry", durations(*sleeps), stderr.String())
			}
		})
	}
	if redirects != 1 {
		t.Fatalf("the redirecting server saw %d polls, want 1", redirects)
	}
}

func TestTransientPollError(t *testing.T) {
	transport := func(op string, err error) error {
		return &url.Error{Op: "Get", URL: "http://zensu.test/api/work-plans/" + plPlan, Err: &net.OpError{Op: op, Net: "tcp", Err: err}}
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"client timeout", &url.Error{Op: "Get", URL: "http://zensu.test", Err: os.ErrDeadlineExceeded}, true},
		{"refused connection", transport("dial", os.NewSyscallError("connect", syscall.ECONNREFUSED)), true},
		{"reset connection", transport("read", os.NewSyscallError("read", syscall.ECONNRESET)), true},
		{"connection closed before the response", &url.Error{Op: "Get", URL: "http://zensu.test", Err: io.EOF}, true},
		{"body cut off after a 2xx", fmt.Errorf("reading response body: %w", io.ErrUnexpectedEOF), true},
		{"rate limited", apiError(429, []byte(`{"code":"rate_limited","message":"slow down"}`)), true},
		{"server error", apiError(500, []byte("boom")), true},
		{"wrapped unavailable", fmt.Errorf("plan: %w", apiError(503, nil)), true},
		{"not found", apiError(404, []byte(`{"code":"not_found","message":"no plan"}`)), false},
		{"conflict", apiError(409, nil), false},
		{"certificate", &url.Error{Op: "Get", URL: "https://zensu.test", Err: &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}}, false},
		{"expired certificate", &url.Error{Op: "Get", URL: "https://zensu.test", Err: x509.CertificateInvalidError{Reason: x509.Expired}}, false},
		{"refused redirect", &url.Error{Op: "Get", URL: "http://evil.test/api", Err: errors.New("refusing cross-host redirect to evil.test")}, false},
		{"invalid API URL", fmt.Errorf("creating request: %w", &url.Error{Op: "parse", URL: "http://[::1", Err: errors.New("missing ']' in host")}), false},
		{"unsupported protocol scheme", &url.Error{Op: "Get", URL: "ftp://zensu.test", Err: errors.New(`unsupported protocol scheme "ftp"`)}, false},
		{"anything else", errTransport, false},
	}
	for _, tc := range cases {
		if got := transientPollError(tc.err); got != tc.want {
			t.Errorf("%s: transientPollError(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

func TestPlanStatus_WatchStopsAtOnceOnClientErrors(t *testing.T) {
	sleeps := stubWatchSleep(t, nil)
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: replySequence(&polls, fakeReply{Status: 404, Body: `{"code":"not_found","message":"no plan"}`}),
	})
	f, _ := testFactory(srv)
	c, stderr := watchCmd(f)
	err := runCmd(t, c, "status", plPlan, "--watch", "--interval", "1s")
	if err == nil || err.Error() != "no plan (status 404)" {
		t.Fatalf("error = %v", err)
	}
	if polls != 1 || len(*sleeps) != 0 || stderr.Len() != 0 {
		t.Fatalf("polls %d, sleeps %q, stderr %q", polls, durations(*sleeps), stderr.String())
	}
}

func TestPlanStatus_WithoutWatchFailsOnTheFirstError(t *testing.T) {
	sleeps := stubWatchSleep(t, nil)
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: replySequence(&polls, unavailable()),
	})
	f, _ := testFactory(srv)
	c, stderr := watchCmd(f)
	if err := runCmd(t, c, "status", plPlan); err == nil || err.Error() != "backend down (status 503)" {
		t.Fatalf("error = %v", err)
	}
	if polls != 1 || len(*sleeps) != 0 || stderr.Len() != 0 {
		t.Fatalf("polls %d, sleeps %q, stderr %q", polls, durations(*sleeps), stderr.String())
	}
}

func TestPlanStatus_WatchEndsQuietlyWhenCancelledDuringBackoff(t *testing.T) {
	sleeps := stubWatchSleep(t, context.Canceled)
	var polls int32
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-plans/" + plPlan: replySequence(&polls, unavailable()),
	})
	f, out := testFactory(srv)
	if err := runCmd(t, NewPlanCmd(f), "status", plPlan, "--watch", "--interval", "1s"); err != nil {
		t.Fatalf("watch: %v", err)
	}
	if polls != 1 || durations(*sleeps) != "1s" || out.Len() != 0 {
		t.Fatalf("polls %d, sleeps %q, output %q", polls, durations(*sleeps), out.String())
	}
}

func TestPlanStatus_WatchPrintsEveryVisibleChange(t *testing.T) {
	order := func(fields map[string]any) map[string]any {
		o := map[string]any{"id": "o1", "status": "pr_open", "attempt": 1}
		for k, v := range fields {
			o[k] = v
		}
		return o
	}
	final := planDetail("final_pr_open")
	finalWithPR := planDetail("final_pr_open")
	finalWithPR["final_pr_url"] = "https://github.com/acme/app/pull/9"
	cases := []struct {
		name          string
		before, after map[string]any
		shows         string
	}{
		{"pull request link", planDetail("open", order(nil)), planDetail("open", order(map[string]any{"pr_url": "https://github.com/acme/app/pull/3"})), "https://github.com/acme/app/pull/3"},
		{"blocked kind", planDetail("open", order(map[string]any{"status": "blocked", "blocked_kind": "plan_approval"})), planDetail("open", order(map[string]any{"status": "blocked", "blocked_kind": "worker_error"})), "blocked(worker_error)"},
		{"attempt", planDetail("open", order(nil)), planDetail("open", order(map[string]any{"attempt": 2})), "2"},
		{"final pull request link", final, finalWithPR, "final PR https://github.com/acme/app/pull/9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubWatchSleep(t, nil)
			var polls int32
			srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
				"GET /api/work-plans/" + plPlan: replySequence(&polls, fakeReply{Body: tc.before}, fakeReply{Body: tc.after}, fakeReply{Body: tc.after}, fakeReply{Body: planDetail("merged")}),
			})
			f, out := testFactory(srv)
			if err := runCmd(t, NewPlanCmd(f), "status", plPlan, "--watch", "--interval", "1s"); err != nil {
				t.Fatalf("watch: %v", err)
			}
			if polls != 4 || strings.Count(out.String(), "Plan "+plPlan) != 3 {
				t.Fatalf("polls %d, output:\n%s", polls, out.String())
			}
			printed := strings.Split(out.String(), "Plan "+plPlan)
			if !strings.Contains(printed[2], tc.shows) || strings.Contains(printed[1], tc.shows) {
				t.Fatalf("the change to %q was not printed:\n%s", tc.shows, out.String())
			}
		})
	}
}

func TestWatchBackoff(t *testing.T) {
	cases := []struct {
		interval time.Duration
		failures int
		want     time.Duration
	}{
		{time.Second, 1, time.Second},
		{time.Second, 2, 2 * time.Second},
		{time.Second, 4, 8 * time.Second},
		{time.Second, 9, 2 * time.Minute},
		{90 * time.Second, 1, 90 * time.Second},
		{90 * time.Second, 2, 2 * time.Minute},
		{5 * time.Minute, 4, 5 * time.Minute},
	}
	for _, tc := range cases {
		if got := watchBackoff(tc.interval, tc.failures); got != tc.want {
			t.Errorf("watchBackoff(%s, %d) = %s, want %s", tc.interval, tc.failures, got, tc.want)
		}
	}
}

func TestPlanStatus_NeedsAClient(t *testing.T) {
	f := &Factory{Out: &bytes.Buffer{}, NewClient: func(context.Context) (*client.Client, error) {
		return nil, errors.New("no login stored")
	}}
	if err := runCmd(t, NewPlanCmd(f), "status", plPlan, "--watch"); err == nil || err.Error() != "no login stored" {
		t.Fatalf("error = %v", err)
	}
}
