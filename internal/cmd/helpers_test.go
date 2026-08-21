package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

var errTransport = errors.New("dial refused by test transport")

type failingRoundTripper struct{}

func (failingRoundTripper) RoundTrip(*http.Request) (*http.Response, error) { return nil, errTransport }

func failingFactory() (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	f := &Factory{
		Out: out,
		NewClient: func(context.Context) (*client.Client, error) {
			cfg := &config.Config{APIKey: "zsk_test"}
			return client.New(cfg, "http://zensu.test", "http://zensu.test/oauth/token",
				client.WithHTTPClient(&http.Client{Transport: failingRoundTripper{}}),
			), nil
		},
	}
	return f, out
}
