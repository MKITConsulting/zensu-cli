package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func testFactory(srv *httptest.Server) (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	f := &Factory{
		Out: out,
		NewClient: func(context.Context) (*client.Client, error) {
			cfg := &config.Config{APIKey: "zsk_test"}
			return client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client())), nil
		},
	}
	return f, out
}

func runCmd(t *testing.T, c interface {
	SetArgs([]string)
	Execute() error
}, args ...string) error {
	t.Helper()
	c.SetArgs(args)
	return c.Execute()
}

func TestParseMethodPath(t *testing.T) {
	tests := []struct {
		args       []string
		wantMethod string
		wantPath   string
	}{
		{[]string{"GET", "/api/x"}, "GET", "/api/x"},
		{[]string{"post", "/api/y"}, "POST", "/api/y"},
		{[]string{"delete", "/z"}, "DELETE", "/z"},
		{[]string{"/api/only"}, "", "/api/only"},
	}
	for _, tc := range tests {
		m, p := parseMethodPath(tc.args)
		if m != tc.wantMethod || p != tc.wantPath {
			t.Errorf("parseMethodPath(%v) = (%q,%q), want (%q,%q)", tc.args, m, p, tc.wantMethod, tc.wantPath)
		}
	}
}

func TestBuildBody(t *testing.T) {
	b, err := buildBody([]string{"name=Acme", "type=public"}, "", nil)
	if err != nil {
		t.Fatalf("buildBody error: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("body not valid JSON: %v (%s)", err, b)
	}
	if got["name"] != "Acme" || got["type"] != "public" {
		t.Errorf("body fields: got %v", got)
	}

	none, err := buildBody(nil, "", nil)
	if err != nil {
		t.Fatalf("buildBody(none) error: %v", err)
	}
	if none != nil {
		t.Errorf("expected nil body, got %q", none)
	}
}

func TestAPICmd_GetRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/products" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []string{"p1"}})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewAPICmd(f)
	if err := runCmd(t, cmd, "GET", "/api/products"); err != nil {
		t.Fatalf("api cmd error: %v", err)
	}
	if !strings.Contains(out.String(), "p1") {
		t.Errorf("output missing response data: %q", out.String())
	}
}

func TestAPICmd_PostWithFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s want POST", r.Method)
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "Acme" {
			t.Errorf("body name: got %q", body["name"])
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "p9", "name": "Acme"})
	}))
	defer srv.Close()

	f, out := testFactory(srv)
	cmd := NewAPICmd(f)
	if err := runCmd(t, cmd, "POST", "/api/products", "-f", "name=Acme"); err != nil {
		t.Fatalf("api cmd error: %v", err)
	}
	if !strings.Contains(out.String(), "p9") {
		t.Errorf("output missing created id: %q", out.String())
	}
}

func TestAPICmd_DefaultsToPOSTWhenBody(t *testing.T) {
	var sawMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewAPICmd(f)
	if err := runCmd(t, cmd, "/api/products", "-f", "name=x"); err != nil {
		t.Fatalf("api cmd error: %v", err)
	}
	if sawMethod != http.MethodPost {
		t.Errorf("default method with body: got %s want POST", sawMethod)
	}
}

func TestAPICmd_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"code": "bad_request", "message": "nope"})
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	cmd := NewAPICmd(f)
	err := runCmd(t, cmd, "GET", "/api/products")
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should surface API message, got %v", err)
	}
}
