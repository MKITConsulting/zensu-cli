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
