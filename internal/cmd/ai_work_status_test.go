package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const (
	statusRoute = "GET /api/ai-work/capture/units/" + progressTestWork + "/status"
	ownerID     = "9dbfeb79-a2bc-4d7a-af64-4ec9dd454490"
)

func ownDetail() func(recordedRequest) fakeReply {
	return reply(200, map[string]any{"id": progressTestWork, "ownerUserId": ownerID, "reportingUserId": ownerID})
}

func plainNotFound(recordedRequest) fakeReply {
	return fakeReply{Status: 404, Body: "404 page not found\n"}
}

func runStatus(t *testing.T, routes map[string]func(recordedRequest) fakeReply, args ...string) (string, string, *fakeAPI, error) {
	t.Helper()
	srv, api := newFakeAPI(t, routes)
	f, out := loginFactory(srv)
	root := NewAIWorkCmd(f)
	root.SetArgs(append([]string{"status"}, args...))
	root.SetErr(&bytes.Buffer{})
	var ran *cobra.Command
	ran, err := root.ExecuteC()
	stderr := &bytes.Buffer{}
	WriteError(stderr, ran, args, err)
	return out.String(), stderr.String(), api, err
}

func TestAIWorkStatus_PrintsTheServersAnswer(t *testing.T) {
	answer := map[string]any{
		"id":     progressTestWork,
		"title":  "Agreed goal",
		"status": map[string]any{"current": "build", "turn": map[string]any{"holder": "agent", "reason": "working"}},
		"facts":  []any{map[string]any{"kind": "turn_started", "source": "session"}},
	}
	out, _, api, err := runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: reply(200, answer)}, progressTestWork)
	if err != nil {
		t.Fatal(err)
	}
	var printed map[string]any
	if json.Unmarshal([]byte(out), &printed) != nil || printed["title"] != "Agreed goal" || printed["status"].(map[string]any)["current"] != "build" {
		t.Errorf("output = %s", out)
	}
	call := api.only(t, "GET", "/api/ai-work/capture/units/"+progressTestWork+"/status")
	if len(call.Query) != 0 || call.Auth != "Bearer browser-login" || call.Raw != "" {
		t.Errorf("the read takes no parameter and travels with the personal login: %+v", call)
	}
}

func TestAIWorkStatus_ResolvesTheReceiptOfTheSession(t *testing.T) {
	aiWorkTestRepo(t)
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	captureDir := filepath.Join(dir, ".git", aiWorkCaptureDirName)
	if err := os.MkdirAll(captureDir, 0o700); err != nil {
		t.Fatal(err)
	}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{statusRoute: reply(200, map[string]any{"id": progressTestWork})})
	f, out := loginFactory(srv)
	receipt := filepath.Join(captureDir, progressTestKey+".json")
	run := func(args ...string) error {
		out.Reset()
		root := NewAIWorkCmd(f)
		root.SetErr(&bytes.Buffer{})
		return runCmd(t, root, append([]string{"status"}, args...)...)
	}

	if err := run("--session", progressTestKey); err == nil || !strings.Contains(err.Error(), "has no capture receipt") {
		t.Errorf("without a receipt the read needs a work id, got %v", err)
	}
	for name, content := range map[string]string{
		"a paused receipt": receiptJSON(srv.URL, `,"capturePaused":true`),
		"a legacy receipt": `{"schema":1,"id":"` + progressTestWork + `","apiUrl":"` + srv.URL + `"}`,
		"a ready receipt":  receiptJSON(srv.URL, ""),
	} {
		if err := os.WriteFile(receipt, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run("--session", progressTestKey); err != nil || !strings.Contains(out.String(), progressTestWork) {
			t.Errorf("%s names the work to read, got %v and %q", name, err, out.String())
		}
	}
	if got := api.count("GET", "/api/ai-work/capture/units/"+progressTestWork+"/status"); got != 3 {
		t.Errorf("status reads = %d, want 3", got)
	}

	for want, content := range map[string]string{
		"cannot be read":              `{"schema":2}`,
		"belongs to another API host": receiptJSON("https://other.example.test", ""),
	} {
		if err := os.WriteFile(receipt, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := run("--session", progressTestKey); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want an error containing %q", err, want)
		}
	}
	for want, args := range map[string][]string{
		"pass a work id, or --session":                 {},
		"pass a work id or --session, not both":        {progressTestWork, "--session", progressTestKey},
		"--session must be the exact plugin-provided":  {"--session", "mine"},
		"expected a canonical non-nil work/event UUID": {"not-an-id"},
		"accepts at most 1 arg":                        {progressTestWork, progressTestWork},
	} {
		if err := run(args...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("status %v: got %v, want an error containing %q", args, err, want)
		}
	}
	if got := len(api.calls()); got != 3 {
		t.Errorf("refused calls sent %d requests", got-3)
	}
}

func TestAIWorkStatus_TellsAMissingRouteFromWorkThatIsNotReadable(t *testing.T) {
	notFound := func(recordedRequest) fakeReply { return refusal(404, "not_found", "work not available", nil) }

	for _, missing := range []func(recordedRequest) fakeReply{plainNotFound, reply(405, "")} {
		out, stderr, api, err := runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: missing, detailRoute: ownDetail()}, progressTestWork, "--json")
		var coded *codedError
		if !errors.As(err, &coded) || coded.code != "unsupported" || out != "" {
			t.Fatalf("a readable own unit and no status route means unsupported, got %v with output %q", err, out)
		}
		want := "error: this server has no work status route yet; update the server to read the status of AI work\n" +
			`{"error":{"code":"unsupported","message":"this server has no work status route yet; update the server to read the status of AI work"}}` + "\n"
		if stderr != want {
			t.Errorf("stderr = %q\nwant     %q", stderr, want)
		}
		if got := api.summary(); len(got) != 2 {
			t.Errorf("requests = %v, want the status read and one detail read", got)
		}
	}

	_, stderr, _, err := runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: plainNotFound, detailRoute: ownDetail()}, progressTestWork)
	if err == nil || stderr != "error: this server has no work status route yet; update the server to read the status of AI work\n" {
		t.Errorf("without --json only the error line is printed, got %q", stderr)
	}

	for name, detail := range map[string]any{
		"another member's unit":   map[string]any{"id": progressTestWork, "ownerUserId": ownerID, "reportingUserId": "891419fa-f7e4-4a52-8123-2bef791876dd"},
		"a unit without an owner": map[string]any{"id": progressTestWork, "ownerUserId": nil, "reportingUserId": ownerID},
	} {
		_, stderr, _, err := runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: plainNotFound, detailRoute: reply(200, detail)}, progressTestWork, "--json")
		var coded *codedError
		if !errors.As(err, &coded) || coded.code != "not_found" || !strings.Contains(stderr, `"code":"not_found"`) || strings.Contains(stderr, "unsupported") {
			t.Errorf("%s is not readable, whatever the server supports, got %v", name, err)
		}
	}

	_, stderr, api, err := runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: notFound, detailRoute: notFound}, progressTestWork, "--json")
	if api404, ok := apiErrorOf(err); !ok || api404.status != 404 || api404.code != "not_found" {
		t.Fatalf("work that is not available is the server's not_found, got %v", err)
	}
	if !strings.HasSuffix(stderr, `{"error":{"status":404,"code":"not_found","message":"work not available"}}`+"\n") || len(api.calls()) != 2 {
		t.Errorf("stderr = %q after %d requests", stderr, len(api.calls()))
	}

	_, _, _, err = runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: plainNotFound, detailRoute: reply(500, map[string]string{"code": "internal_error", "message": "failed to process work result"})}, progressTestWork)
	if api500, ok := apiErrorOf(err); !ok || api500.status != 500 {
		t.Errorf("a detail read that fails decides nothing and is reported, got %v", err)
	}

	_, _, api, err = runStatus(t, map[string]func(recordedRequest) fakeReply{statusRoute: func(recordedRequest) fakeReply {
		return refusal(403, "forbidden", "work capture requires a personal first-party CLI OAuth login", nil)
	}}, progressTestWork)
	if err == nil || !strings.Contains(err.Error(), "(status 403, code forbidden)") || len(api.calls()) != 1 {
		t.Errorf("any other refusal is reported as it is, without a detail read, got %v", err)
	}
}

func TestAIWorkStatus_RefusesEveryCredentialButThePersonalLogin(t *testing.T) {
	for _, cfg := range []*config.Config{{APIKey: "zsk_shared"}, {AccessToken: "zst_worker"}, {}} {
		srv, api := newFakeAPI(t, nil)
		f, _ := testFactory(srv)
		f.NewClient = func(context.Context) (*client.Client, error) { return client.New(cfg, srv.URL, ""), nil }
		if err := runCmd(t, NewAIWorkCmd(f), "status", progressTestWork); err == nil || len(api.calls()) != 0 {
			t.Errorf("credential %+v must be refused before any request, got %v", cfg, err)
		}
	}
}
