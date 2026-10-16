package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestAPIError_NamesOnlyAPrintableCodeInTheStatusSuffix(t *testing.T) {
	longest := "a" + strings.Repeat("b", 63)
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantText  string
		wantPlain string
	}{
		{"code present", 409, `{"code":"stale_attempt","message":"the order is stale"}`, "the order is stale (status 409, code stale_attempt)", "the order is stale"},
		{"code absent", 404, `{"message":"no plan"}`, "no plan (status 404)", "no plan"},
		{"empty code", 404, `{"code":"","message":"no plan"}`, "no plan (status 404)", "no plan"},
		{"uppercase code", 409, `{"code":"Stale_Attempt","message":"m"}`, "m (status 409)", "m"},
		{"code with a leading digit", 409, `{"code":"1stale","message":"m"}`, "m (status 409)", "m"},
		{"code that would rewrite the suffix", 409, `{"code":"ok) (status 200, code ok","message":"m"}`, "m (status 409)", "m"},
		{"code with an escape sequence", 409, `{"code":"stale\u001b[2J","message":"m"}`, "m (status 409)", "m"},
		{"code with a trailing newline", 409, `{"code":"stale\n","message":"m"}`, "m (status 409)", "m"},
		{"code of 64 characters", 409, `{"code":"` + longest + `","message":"m"}`, "m (status 409, code " + longest + ")", "m"},
		{"code of 65 characters", 409, `{"code":"` + longest + `b","message":"m"}`, "m (status 409)", "m"},
		{"body without a JSON message", 502, "upstream exploded", "request failed (status 502): upstream exploded", "request failed: upstream exploded"},
		{"JSON body with a code and no message", 409, `{"code":"stale_attempt"}`, `request failed (status 409, code stale_attempt): {"code":"stale_attempt"}`, `request failed: {"code":"stale_attempt"}`},
		{"JSON body with an invalid code and no message", 409, `{"code":"Stale"}`, `request failed (status 409): {"code":"Stale"}`, `request failed: {"code":"Stale"}`},
		{"empty body", 504, "", "request failed (status 504): ", "request failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, ok := apiErrorOf(apiError(tc.status, []byte(tc.body)))
			if !ok {
				t.Fatal("apiError must return an API status error")
			}
			if e.Error() != tc.wantText {
				t.Errorf("text = %q, want %q", e.Error(), tc.wantText)
			}
			if e.plain != tc.wantPlain {
				t.Errorf("message without the status suffix = %q, want %q", e.plain, tc.wantPlain)
			}
			if e.status != tc.status {
				t.Errorf("status = %d, want %d", e.status, tc.status)
			}
		})
	}
}

func TestAPIError_KeepsTheServersCodeAndTheMessageFirstForCallers(t *testing.T) {
	versionRefusal, _ := apiErrorOf(apiError(400, []byte(`{"code":"invalid_body","message":"unknown field \"expectedUpdatedAt\""}`)))
	if versionRefusal.code != "invalid_body" || versionRefusal.status != 400 || !strings.HasPrefix(versionRefusal.text, policyVersionUnknown) {
		t.Errorf("callers branch on the code, the status and the message prefix, got code %q status %d text %q", versionRefusal.code, versionRefusal.status, versionRefusal.text)
	}
	unprintable, _ := apiErrorOf(apiError(409, []byte(`{"code":"Not-A-Code","message":"m"}`)))
	if unprintable.code != "Not-A-Code" {
		t.Errorf("the code a caller compares must stay the server's value, got %q", unprintable.code)
	}
}

func jsonFlagCommand(on bool) *cobra.Command {
	c := &cobra.Command{Use: "get"}
	c.Flags().Bool("json", false, "output raw JSON")
	if on {
		_ = c.Flags().Set("json", "true")
	}
	return c
}

func assertJSONErrorIsTheLastLine(t *testing.T, stderr string) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	var line struct {
		Error map[string]any `json:"error"`
	}
	dec := json.NewDecoder(strings.NewReader(lines[len(lines)-1]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&line); err != nil {
		t.Fatalf("the last stderr line must be the JSON error line, got %q: %v", lines[len(lines)-1], err)
	}
	if _, ok := line.Error["message"].(string); !ok {
		t.Fatalf("the JSON error line must carry a message, got %v", line.Error)
	}
	for key := range line.Error {
		if key != "status" && key != "code" && key != "message" {
			t.Fatalf("the JSON error line carries an unexpected field %q", key)
		}
	}
}

func TestWriteError_PrintsTheErrorLineAndUnderJSONOneJSONLine(t *testing.T) {
	stale := apiError(409, []byte(`{"code":"stale_attempt","message":"the order is held by another attempt; stop"}`))
	flagErr := &flagParseError{err: errors.New("unknown flag: --bogus")}
	for _, tc := range []struct {
		name string
		ran  *cobra.Command
		args []string
		err  error
		want string
	}{
		{"code present without --json", jsonFlagCommand(false), nil, stale,
			"error: the order is held by another attempt; stop (status 409, code stale_attempt)\n"},
		{"code present with --json", jsonFlagCommand(true), nil, stale,
			"error: the order is held by another attempt; stop (status 409, code stale_attempt)\n" +
				`{"error":{"status":409,"code":"stale_attempt","message":"the order is held by another attempt; stop"}}` + "\n"},
		{"code absent with --json", jsonFlagCommand(true), nil, apiError(404, []byte(`{"message":"no plan"}`)),
			"error: no plan (status 404)\n" + `{"error":{"status":404,"message":"no plan"}}` + "\n"},
		{"invalid code with --json", jsonFlagCommand(true), nil, apiError(409, []byte(`{"code":"ok) (status 200","message":"m"}`)),
			"error: m (status 409)\n" + `{"error":{"status":409,"message":"m"}}` + "\n"},
		{"wrapped API error with --json", jsonFlagCommand(true), nil,
			fmt.Errorf("item 3: %w; created before the failure: revision v2 of ZEN-7", apiError(409, []byte(`{"code":"conflict","message":"slug taken"}`))),
			"error: item 3: slug taken (status 409, code conflict); created before the failure: revision v2 of ZEN-7\n" +
				`{"error":{"status":409,"code":"conflict","message":"item 3: slug taken; created before the failure: revision v2 of ZEN-7"}}` + "\n"},
		{"body without a JSON message with --json", jsonFlagCommand(true), nil, apiError(502, []byte("upstream <b>exploded</b> & gone")),
			"error: request failed (status 502): upstream <b>exploded</b> & gone\n" +
				`{"error":{"status":502,"message":"request failed: upstream <b>exploded</b> & gone"}}` + "\n"},
		{"body without a JSON message but with a code with --json", jsonFlagCommand(true), nil, apiError(409, []byte(`{"code":"stale_attempt"}`)),
			`error: request failed (status 409, code stale_attempt): {"code":"stale_attempt"}` + "\n" +
				`{"error":{"status":409,"code":"stale_attempt","message":"request failed: {\"code\":\"stale_attempt\"}"}}` + "\n"},
		{"validation error with --json", jsonFlagCommand(true), nil, errors.New("--title is required"),
			"error: --title is required\n" + `{"error":{"message":"--title is required"}}` + "\n"},
		{"validation error without --json", jsonFlagCommand(false), nil, errors.New("--title is required"),
			"error: --title is required\n"},
		{"network error with --json", jsonFlagCommand(true), nil, fmt.Errorf("Get \"http://zensu.test/api\": %w", errTransport),
			"error: Get \"http://zensu.test/api\": dial refused by test transport\n" +
				`{"error":{"message":"Get \"http://zensu.test/api\": dial refused by test transport"}}` + "\n"},
		{"multi-line message with --json", jsonFlagCommand(true), nil, errors.New("stopped watching:\npoll 1 failed\npoll 2 failed\n"),
			"error: stopped watching:\npoll 1 failed\npoll 2 failed\n\n" +
				`{"error":{"message":"stopped watching:\npoll 1 failed\npoll 2 failed\n"}}` + "\n"},
		{"terminal control sequences with --json", jsonFlagCommand(true), nil, errors.New("bad \x1b[2Jinput‮"),
			"error: bad [2Jinput\n" + `{"error":{"message":"bad [2Jinput"}}` + "\n"},
		{"command without a json flag", &cobra.Command{Use: "status"}, []string{"status", "--json"}, flagErr,
			"error: unknown flag: --bogus\n"},
		{"no command", nil, nil, errors.New("boom"),
			"error: boom\n"},
		{"flag error before --json was parsed", jsonFlagCommand(false), []string{"work", "get", woID, "--bogus", "--json"}, flagErr,
			"error: unknown flag: --bogus\n" + `{"error":{"message":"unknown flag: --bogus"}}` + "\n"},
		{"flag error before --json=true was parsed", jsonFlagCommand(false), []string{"get", "--bogus", "--json=true"}, flagErr,
			"error: unknown flag: --bogus\n" + `{"error":{"message":"unknown flag: --bogus"}}` + "\n"},
		{"flag error with --json=false", jsonFlagCommand(false), []string{"get", "--bogus", "--json=false"}, flagErr,
			"error: unknown flag: --bogus\n"},
		{"flag error with --json after the terminator", jsonFlagCommand(false), []string{"get", "--bogus", "--", "--json"}, flagErr,
			"error: unknown flag: --bogus\n"},
		{"--json as the value of another flag", jsonFlagCommand(false), []string{"create", "--title", "--json"}, errors.New("--product is required"),
			"error: --product is required\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stderr := &bytes.Buffer{}
			WriteError(stderr, tc.ran, tc.args, tc.err)
			if stderr.String() != tc.want {
				t.Fatalf("stderr = %q\nwant     %q", stderr.String(), tc.want)
			}
			if strings.Contains(tc.want, `{"error":`) {
				assertJSONErrorIsTheLastLine(t, stderr.String())
			}
		})
	}
}

func TestWriteError_FollowsTheJSONFlagNotTheDefaultOutput(t *testing.T) {
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/work-orders/" + woID: reply(409, `{"code":"stale_attempt","message":"stop without further forge writes"}`),
	})
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"work get prints JSON by default", []string{"get", woID},
			"error: stop without further forge writes (status 409, code stale_attempt)\n"},
		{"work get --json", []string{"get", woID, "--json"},
			"error: stop without further forge writes (status 409, code stale_attempt)\n" +
				`{"error":{"status":409,"code":"stale_attempt","message":"stop without further forge writes"}}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, _ := testFactory(srv)
			work := NewWorkCmd(f)
			work.SetArgs(tc.args)
			work.SetErr(&bytes.Buffer{})
			ran, err := work.ExecuteC()
			if err == nil {
				t.Fatal("the refused read must fail")
			}
			stderr := &bytes.Buffer{}
			WriteError(stderr, ran, tc.args, err)
			if stderr.String() != tc.want {
				t.Fatalf("stderr = %q\nwant     %q", stderr.String(), tc.want)
			}
		})
	}
}

func TestJSONByDefaultCommands_AcceptJSONWhichOnlyAddsTheErrorLine(t *testing.T) {
	groups := map[string]func(*Factory) *cobra.Command{
		"doc":       NewDocCmd,
		"features":  NewFeaturesCmd,
		"ghost":     NewGhostCmd,
		"journeys":  NewJourneysCmd,
		"knowledge": NewKnowledgeCmd,
		"products":  NewProductsCmd,
		"pulse":     NewPulseCmd,
		"roadmap":   NewRoadmapCmd,
		"security":  NewSecurityCmd,
		"tiers":     NewTiersCmd,
		"work":      NewWorkCmd,
	}
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"path":"` + r.URL.Path + `"}`))
	}))
	defer ok.Close()
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"not_found","message":"no such record"}`))
	}))
	defer refused.Close()
	run := func(srv *httptest.Server, args []string) (string, string, error) {
		f, out := testFactory(srv)
		group := groups[args[0]](f)
		group.SetArgs(args[1:])
		group.SetOut(&bytes.Buffer{})
		group.SetErr(&bytes.Buffer{})
		ran, err := group.ExecuteC()
		stderr := &bytes.Buffer{}
		WriteError(stderr, ran, args[1:], err)
		return out.String(), stderr.String(), err
	}
	const errorLine = "error: no such record (status 404, code not_found)\n"
	const jsonErrorLine = `{"error":{"status":404,"code":"not_found","message":"no such record"}}` + "\n"

	for _, args := range [][]string{
		{"doc", "claude-md-context", "--product", "p1"},
		{"doc", "gen-context", "f1", "--doc-type", "overview"},
		{"features", "get", "f1"},
		{"features", "history", "f1"},
		{"ghost", "candidates", "s1", "--product", "p1"},
		{"journeys", "get", "j1", "--product", "p1"},
		{"journeys", "health", "j1", "--product", "p1"},
		{"journeys", "suggest", "--product", "p1"},
		{"knowledge", "get", "k1"},
		{"products", "get", "p1"},
		{"products", "vision-get", "v1"},
		{"pulse", "summary", pulseTestSessionID},
		{"roadmap", "get", "r1"},
		{"security", "analyze", "f1"},
		{"security", "posture", "p1"},
		{"security", "score", "f1"},
		{"security", "suggest-tests", "f1"},
		{"security", "threat-model", "f1"},
		{"security", "validate", "f1"},
		{"tiers", "matrix", "--product", "p1"},
		{"work", "get", woID},
		{"work", "policy", "get", "--product", woProduct},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			withJSON := append(append([]string{}, args...), "--json")
			plain, _, err := run(ok, args)
			if err != nil {
				t.Fatalf("without --json: %v", err)
			}
			got, _, err := run(ok, withJSON)
			if err != nil {
				t.Fatalf("with --json: %v", err)
			}
			if got != plain || !json.Valid([]byte(plain)) {
				t.Fatalf("--json must keep the JSON output unchanged:\nwith    %q\nwithout %q", got, plain)
			}
			if _, stderr, _ := run(refused, args); stderr != errorLine {
				t.Errorf("stderr without --json = %q, want %q", stderr, errorLine)
			}
			if _, stderr, _ := run(refused, withJSON); stderr != errorLine+jsonErrorLine {
				t.Errorf("stderr with --json = %q, want %q", stderr, errorLine+jsonErrorLine)
			}
		})
	}
}

func TestNewRootCmd_WritesTheJSONErrorLineForFlagAndArgumentErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag before --json", []string{"work", "get", woID, "--bogus", "--json"},
			"error: unknown flag: --bogus\n" + `{"error":{"message":"unknown flag: --bogus"}}` + "\n"},
		{"unknown flag after --json", []string{"work", "get", woID, "--json", "--bogus"},
			"error: unknown flag: --bogus\n" + `{"error":{"message":"unknown flag: --bogus"}}` + "\n"},
		{"unknown flag without --json", []string{"work", "get", woID, "--bogus"},
			"error: unknown flag: --bogus\n"},
		{"missing argument with --json", []string{"work", "get", "--json"},
			"error: accepts 1 arg(s), received 0\n" + `{"error":{"message":"accepts 1 arg(s), received 0"}}` + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := NewRootCmd()
			root.SetArgs(tc.args)
			root.SetOut(&bytes.Buffer{})
			root.SetErr(&bytes.Buffer{})
			ran, err := root.ExecuteC()
			if err == nil {
				t.Fatal("the invocation must fail before it reaches the API")
			}
			stderr := &bytes.Buffer{}
			WriteError(stderr, ran, tc.args, err)
			if stderr.String() != tc.want {
				t.Fatalf("stderr = %q\nwant     %q", stderr.String(), tc.want)
			}
		})
	}
}
