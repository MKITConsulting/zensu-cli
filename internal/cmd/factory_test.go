package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

func TestPrintJSON_FallsBackToRawBytes(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printJSON(out, []byte("not json at all")); err != nil {
		t.Fatalf("printJSON error: %v", err)
	}
	if got := out.String(); got != "not json at all\n" {
		t.Errorf("printJSON should pass undecodable bytes through verbatim: got %q", got)
	}
}

func TestPrintJSON_IndentsValidJSON(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printJSON(out, []byte(`{"id":"m1"}`)); err != nil {
		t.Fatalf("printJSON error: %v", err)
	}
	if got := out.String(); got != "{\n  \"id\": \"m1\"\n}\n" {
		t.Errorf("printJSON should indent valid JSON: got %q", got)
	}
}

func TestRequest_SendsJSONContentType(t *testing.T) {
	var gotType string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	if _, err := f.request(context.Background(), http.MethodPost, "/api/products", []byte(`{"name":"p"}`)); err != nil {
		t.Fatalf("request error: %v", err)
	}
	if gotType != "application/json" {
		t.Errorf("every JSON write must declare application/json: got %q", gotType)
	}
	if string(gotBody) != `{"name":"p"}` {
		t.Errorf("body: got %q", gotBody)
	}
}

func TestRequestWithContentType_SendsCallerContentType(t *testing.T) {
	const boundaryType = "multipart/form-data; boundary=zensu-test"
	var gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"m1"}`))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	if _, err := f.requestWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", boundaryType, []byte("payload")); err != nil {
		t.Fatalf("requestWithContentType error: %v", err)
	}
	if gotType != boundaryType {
		t.Errorf("Content-Type: got %q want %q", gotType, boundaryType)
	}
}

func TestRequest_SurfacesClientConstructionError(t *testing.T) {
	wantErr := errors.New("no session — run zensu auth login")
	f := &Factory{
		Out:       &bytes.Buffer{},
		NewClient: func(context.Context) (*client.Client, error) { return nil, wantErr },
	}
	_, err := f.request(context.Background(), http.MethodGet, "/api/products", nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("request should surface the client construction error, got: %v", err)
	}
}

func TestRequestWithContentType_SurfacesClientConstructionError(t *testing.T) {
	wantErr := errors.New("no session — run zensu auth login")
	f := &Factory{
		Out:       &bytes.Buffer{},
		NewClient: func(context.Context) (*client.Client, error) { return nil, wantErr },
	}
	_, err := f.requestWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("body"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("requestWithContentType should surface the client construction error, got: %v", err)
	}
}

func TestRequestWithContentType_SurfacesTransportError(t *testing.T) {
	f, _ := failingFactory()
	_, err := f.requestWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("body"))
	if !errors.Is(err, errTransport) {
		t.Fatalf("requestWithContentType should surface the transport error unchanged, got: %v", err)
	}
	if strings.Contains(err.Error(), "status") {
		t.Errorf("a transport failure must not be reported as an API status error, got: %v", err)
	}
}

func TestSanitizeTerminal_StripsControlAndBidiRunes(t *testing.T) {
	got := sanitizeTerminal("safe\u001b[31mred\u0007\u009bmore\u007f\u202egnp.exe\u200bx\u061cy\u2060z\ufff9w\U000e0041v\ttab")
	for _, bad := range []rune{0x1b, 0x07, 0x9b, 0x7f, 0x202e, 0x200b, 0x061c, 0x2060, 0xfff9, 0xe0041} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("rune %#U must be stripped, got %q", bad, got)
		}
	}
	if strings.ContainsRune(got, '\t') {
		t.Errorf("a tab inside a value would open an extra column and must become a space, got %q", got)
	}
	if !strings.Contains(got, " tab") {
		t.Errorf("the tab must be replaced by a space rather than dropped, got %q", got)
	}
	for _, keep := range []string{"safe", "red", "more", "gnp.exe", "tab"} {
		if !strings.Contains(got, keep) {
			t.Errorf("printable text %q must survive, got %q", keep, got)
		}
	}
}

func TestSanitizeTerminal_KeepsJoinersThatScriptsNeed(t *testing.T) {
	got := sanitizeTerminal("\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645 \U0001f468\u200d\U0001f469\u200d\U0001f467")
	for _, keep := range []rune{0x200c, 0x200d} {
		if !strings.ContainsRune(got, keep) {
			t.Errorf("rune %#U must survive, got %q", keep, got)
		}
	}
}

func TestAPIError_SanitizesServerText(t *testing.T) {
	withMessage := apiError(400, []byte(`{"code":"bad_request","message":"bad \u001b[2Jinput"}`))
	if strings.ContainsRune(withMessage.Error(), 0x1b) {
		t.Errorf("the API message must not carry escape sequences to the terminal: %q", withMessage.Error())
	}
	if !strings.Contains(withMessage.Error(), "status 400") {
		t.Errorf("the status must survive sanitization: %q", withMessage.Error())
	}
	rawFallback := apiError(502, []byte("upstream \u001b[2Jexploded"))
	if strings.ContainsRune(rawFallback.Error(), 0x1b) {
		t.Errorf("the raw-body fallback must be sanitized too: %q", rawFallback.Error())
	}
	if !strings.Contains(rawFallback.Error(), "exploded") {
		t.Errorf("the raw body text must survive: %q", rawFallback.Error())
	}
}

func TestPrintJSON_SanitizesTheNonJSONFallback(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printJSON(out, []byte("not json \u001b[2J at all")); err != nil {
		t.Fatalf("printJSON error: %v", err)
	}
	if strings.ContainsRune(out.String(), 0x1b) {
		t.Errorf("the undecodable fallback must not emit escape sequences, got %q", out.String())
	}
	if !strings.Contains(out.String(), "not json") {
		t.Errorf("the printable body must survive, got %q", out.String())
	}
}

func TestPrintJSON_KeepsValidJSONByteExact(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printJSON(out, []byte(`{"title":"a\u001b[2Jb"}`)); err != nil {
		t.Fatalf("printJSON error: %v", err)
	}
	if !strings.Contains(out.String(), `\u001b`) {
		t.Errorf("valid JSON is a machine contract and must keep its escapes verbatim, got %q", out.String())
	}
}

func TestPrintJSON_KeepsLineStructureInTheNonJSONFallback(t *testing.T) {
	out := &bytes.Buffer{}
	if err := printJSON(out, []byte("first line\nsecond \u001b[2Jline\rthird")); err != nil {
		t.Fatalf("printJSON error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "first line\nsecond") {
		t.Errorf("this fallback is the only escape hatch for a body the CLI cannot parse, so a multi-line body must keep its line breaks instead of collapsing onto one line, got %q", got)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("the escape sequence must still be stripped, got %q", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Errorf("a bare CR can overwrite an already-printed line, so unlike LF it must still be dropped, got %q", got)
	}
}

func TestAPIError_StaysOnOneLineWhenTheServerBodyHasNewlines(t *testing.T) {
	err := apiError(500, []byte("upstream failed\nstack frame 1\nstack frame 2"))
	if strings.ContainsRune(err.Error(), '\n') {
		t.Errorf("an error string must stay on one line however many newlines the server body carried, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "stack frame 1") {
		t.Errorf("the body text must survive the collapse, got %q", err.Error())
	}
	withMessage := apiError(400, []byte(`{"code":"bad_request","message":"first\nsecond"}`))
	if strings.ContainsRune(withMessage.Error(), '\n') {
		t.Errorf("the decoded message branch must collapse newlines too, got %q", withMessage.Error())
	}
}

func TestSanitizeUploadFileName_DropsNewlinesSoNoPartHeaderCanBeInjected(t *testing.T) {
	got := sanitizeUploadFileName("hero\r\nContent-Type: text/html\n.png")
	for _, bad := range []rune{'\r', '\n'} {
		if strings.ContainsRune(got, bad) {
			t.Errorf("rune %#U in a multipart filename= value would frame an extra part header, so the wire name has its own sanitizer and must drop it whatever the display sanitizers do, got %q", bad, got)
		}
	}
	if strings.ContainsRune(sanitizeUploadFileName("shot\u001b[2J.png"), 0x1b) {
		t.Error("the transmitted name must still lose terminal control sequences")
	}
	if got := sanitizeUploadFileName("hero.png"); got != "hero.png" {
		t.Errorf("an ordinary file name must pass through unchanged, got %q", got)
	}
	if got := sanitizeUploadFileName("a\tb.png"); got != "ab.png" {
		t.Errorf("a tab in a transmitted file name must be dropped, not rewritten: the display sanitizer turns it into a space to keep a table column aligned, which would silently rename the uploaded file, got %q", got)
	}
	if got := sanitizeUploadFileName(`a"quote\slash.png`); got != "aquoteslash.png" {
		t.Errorf("the part header must not depend on mime/multipart escaping the quote and backslash for us, got %q", got)
	}
}

func TestRequestWithContentType_PrefersTheAPIErrorWhenANon2xxBodyIsAlsoTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"upstream`))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	_, err := f.requestWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err == nil {
		t.Fatal("a 500 must still surface as an error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("the status is the more informative fact and must win over the truncation, got: %v", err)
	}
	if strings.Contains(err.Error(), "reading response body") {
		t.Errorf("swapping the status branch and the read-error branch must not go unnoticed, got: %v", err)
	}
}

func TestReadResponse_ReportsATruncatedBodyAsATransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"m1"`))
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	_, err := f.requestWithContentType(context.Background(), http.MethodPost, "/api/features/f1/mocks", "multipart/form-data; boundary=b", []byte("payload"))
	if err == nil {
		t.Fatal("a 2xx whose body was cut short must not be reported as success: the caller would blame a malformed body for a transport failure and re-run an upload the server already accepted")
	}
	if !strings.Contains(err.Error(), "reading response body") {
		t.Errorf("the error must name the response read so it is distinguishable from a JSON decode failure, got: %v", err)
	}
	if errors.Unwrap(err) == nil {
		t.Error("the transport cause must stay wrapped: swapping the wrapping verb for a plain one would break errors.Is for every caller without failing a test")
	}
}
