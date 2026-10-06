package cmd

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestLoginWithToken_ValidatesAndPersists(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "zsk_valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "login", "--api-url", srv.URL, "--with-token", "zsk_valid"); err != nil {
		t.Fatalf("login --with-token error: %v", err)
	}
	if !strings.Contains(out.String(), "API key") {
		t.Errorf("expected success message, got %q", out.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.APIKey != "zsk_valid" {
		t.Errorf("APIKey not persisted: %+v", cfg)
	}
	if cfg.APIURL != srv.URL {
		t.Errorf("APIURL: got %q want %q", cfg.APIURL, srv.URL)
	}
}

func TestLoginWithToken_AcceptsAnAgentKey(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"agent_key_not_allowed","message":"an agent key serves only the work order worker routes"}`))
	}))
	defer srv.Close()

	f, out := authFactory()
	if err := runCmd(t, NewAuthCmd(f), "login", "--api-url", srv.URL, "--with-token", "zsk_agent"); err != nil {
		t.Fatalf("login with an agent key: %v", err)
	}
	if out.String() != "Logged in to "+srv.URL+" with an agent key; it serves only the worker verbs zensu work claim, confirm, release and usage, and claims only in products whose automation policy allows it: zensu work policy set --product <product id> --add-allowed-key <key id>.\n" {
		t.Fatalf("output = %q", out.String())
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.APIKey != "zsk_agent" || cfg.APIURL != srv.URL {
		t.Fatalf("agent key not persisted: %+v", cfg)
	}
}

type failingLoginInput struct{}

func (failingLoginInput) Read([]byte) (int, error) { return 0, errors.New("stdin closed") }

func TestLoginWithToken_ReadsTheKeyFromStdin(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	var key string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	cmd.SetIn(strings.NewReader("  zsk_from_stdin\n"))
	if err := runCmd(t, cmd, "login", "--api-url", srv.URL, "--with-token", "-"); err != nil {
		t.Fatalf("login --with-token -: %v", err)
	}
	if key != "zsk_from_stdin" || out.String() != "Logged in to "+srv.URL+" with API key.\n" {
		t.Fatalf("key = %q output = %q", key, out.String())
	}
	cfg, err := config.Load()
	if err != nil || cfg.APIKey != "zsk_from_stdin" {
		t.Fatalf("stored key = %+v (%v)", cfg, err)
	}
}

func TestLoginWithToken_RefusesAnEmptyOrUnreadableKey(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, tc := range []struct {
		value string
		in    io.Reader
		want  string
	}{
		{"", strings.NewReader(""), "--with-token requires an API key value (or - to read from stdin)"},
		{"-", strings.NewReader(" \n"), "--with-token requires an API key value (or - to read from stdin)"},
		{"-", failingLoginInput{}, "stdin closed"},
	} {
		f, out := authFactory()
		cmd := NewAuthCmd(f)
		cmd.SetIn(tc.in)
		if err := runCmd(t, cmd, "login", "--api-url", srv.URL, "--with-token", tc.value); err == nil || err.Error() != tc.want || out.Len() != 0 {
			t.Errorf("--with-token %q: error = %v output = %q, want %q", tc.value, err, out.String(), tc.want)
		}
	}
	if calls != 0 {
		t.Fatalf("validation requests = %d, want none for a missing key", calls)
	}
}

func TestLoginWithToken_RejectsBadKey(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	f, _ := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "login", "--api-url", srv.URL, "--with-token", "zsk_bad"); err == nil {
		t.Fatal("login with invalid key should error")
	}
}
