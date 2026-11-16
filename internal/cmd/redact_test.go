package cmd

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func echoingServer(t *testing.T, status int, body func(r *http.Request) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body(r)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func factoryWith(srv *httptest.Server, cfg *config.Config) *Factory {
	return &Factory{
		Out: &bytes.Buffer{},
		NewClient: func(context.Context) (*client.Client, error) {
			return client.New(cfg, srv.URL, srv.URL+"/oauth/token", client.WithHTTPClient(srv.Client())), nil
		},
	}
}

func TestRedactCredentials_APIErrorsNeverEchoTheSentAPIKey(t *testing.T) {
	const key = "zsk_EXAMPLE_echoed_key"
	for _, tc := range []struct {
		name   string
		status int
		body   func(r *http.Request) string
		want   string
	}{
		{
			name:   "message",
			status: http.StatusBadRequest,
			body: func(r *http.Request) string {
				return `{"code":"bad_key","message":"key ` + r.Header.Get("X-API-Key") + ` is not valid here"}`
			},
			want: "key *** is not valid here (status 400, code bad_key)",
		},
		{
			name:   "body without a JSON message",
			status: http.StatusBadGateway,
			body: func(r *http.Request) string {
				return "proxy refused X-API-Key: " + r.Header.Get("X-API-Key")
			},
			want: "request failed (status 502): proxy refused X-API-Key: ***",
		},
		{
			name:   "code",
			status: http.StatusForbidden,
			body: func(r *http.Request) string {
				return `{"code":"` + r.Header.Get("X-API-Key") + `","message":"nope"}`
			},
			want: "nope (status 403)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := factoryWith(echoingServer(t, tc.status, tc.body), &config.Config{APIKey: key})
			_, err := f.request(context.Background(), http.MethodGet, "/api/products", nil)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			e, ok := apiErrorOf(err)
			if !ok {
				t.Fatalf("error %T is not an API error", err)
			}
			for field, value := range map[string]string{"text": e.text, "plain": e.plain, "raw": string(e.raw), "code": e.code} {
				if strings.Contains(value, key) {
					t.Errorf("%s holds the API key: %q", field, value)
				}
			}
			var stderr bytes.Buffer
			cmd := NewProductsCmd(f)
			cmd.SetArgs([]string{"list", "--json"})
			ran, runErr := cmd.ExecuteC()
			WriteError(&stderr, ran, []string{"list", "--json"}, runErr)
			if runErr == nil || strings.Contains(stderr.String(), key) || !strings.Contains(stderr.String(), "error: "+tc.want+"\n") {
				t.Fatalf("printed error = %q (err %v), want %q with the key redacted", stderr.String(), runErr, tc.want)
			}
		})
	}
}

func TestRedactCredentials_CoversTheJSONEscapedForm(t *testing.T) {
	const key = "zsk_<EXAMPLE&key>"
	srv := echoingServer(t, http.StatusConflict, func(*http.Request) string {
		return `{"message":"key zsk_<EXAMPLE&key> is taken","detail":"zsk_<EXAMPLE&key>"}`
	})
	f := factoryWith(srv, &config.Config{APIKey: key})
	_, err := f.request(context.Background(), http.MethodGet, "/api/products", nil)
	e, ok := apiErrorOf(err)
	if !ok || e.plain != "key *** is taken" {
		t.Fatalf("error = %v, want the decoded key redacted", err)
	}
	if strings.Contains(string(e.raw), `zsk_<EXAMPLE&key>`) || strings.Contains(string(e.raw), key) {
		t.Fatalf("raw body keeps the key: %s", e.raw)
	}
}

func TestRedactCredentials_APIErrorsNeverEchoTheBearerToken(t *testing.T) {
	const token = "EXAMPLE-access-token"
	srv := echoingServer(t, http.StatusUnprocessableEntity, func(r *http.Request) string {
		return `{"code":"invalid","message":"got ` + r.Header.Get("Authorization") + `"}`
	})
	f := factoryWith(srv, &config.Config{AccessToken: token, ExpiresAt: time.Now().Add(time.Hour)})
	_, err := f.request(context.Background(), http.MethodGet, "/api/products", nil)
	if err == nil || err.Error() != "got Bearer *** (status 422, code invalid)" {
		t.Fatalf("error = %v, want the token redacted", err)
	}
}

func TestRedactCredentials_LeavesErrorsWithoutTheCredentialUnchanged(t *testing.T) {
	srv := echoingServer(t, http.StatusNotFound, func(*http.Request) string {
		return `{"code":"not_found","message":"product not found"}`
	})
	f := factoryWith(srv, &config.Config{APIKey: "zsk_EXAMPLE_echoed_key"})
	_, err := f.request(context.Background(), http.MethodGet, "/api/products/p1", nil)
	if err == nil || err.Error() != "product not found (status 404, code not_found)" {
		t.Fatalf("error = %v", err)
	}
	if got := apiError(400, []byte(`{"message":"plain"}`)).Error(); got != "plain (status 400)" {
		t.Fatalf("apiError without credentials = %q", got)
	}
}
