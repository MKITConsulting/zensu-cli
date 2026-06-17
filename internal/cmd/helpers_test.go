package cmd

import (
	"bytes"
	"context"
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
