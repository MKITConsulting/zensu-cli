package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/spf13/cobra"
)

func TestAIWorkBindingFailureBoundaries(t *testing.T) {
	aiWorkTestRepo(t)
	const id = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	const other = "891419fa-f7e4-4a52-8123-2bef791876dd"
	mode := "valid"
	srv, _ := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/ai-work/capture/context": func(r recordedRequest) fakeReply {
			if mode == "failure" {
				return fakeReply{Status: 403, Body: map[string]any{"message": "denied"}}
			}
			product, user := r.Query.Get("productId"), id
			if mode == "switch" && r.Query.Get("featureId") != "" {
				user = other
			}
			return fakeReply{Status: 200, Body: map[string]any{"productId": product, "reportingUserId": user, "organizationId": id, "featureId": r.Query.Get("featureId"), "featureRevisionId": r.Query.Get("featureRevisionId")}}
		},
	})
	f, _ := testFactory(srv)
	c := client.New(&config.Config{AccessToken: "personal"}, srv.URL, "", client.WithHTTPClient(srv.Client()))
	f.NewClient = func(context.Context) (*client.Client, error) { return c, nil }
	run := func(args ...string) error { return runCmd(t, NewAIWorkCmd(f), args...) }
	mustFail := func(err error) {
		t.Helper()
		if err == nil {
			t.Fatal("unsafe capture accepted")
		}
	}
	mustFail(run("bind", "--product", "bad", "--confirm"))
	for _, scope := range [][3]string{{"bad", "", ""}, {"", "bad", ""}, {"", "", id}} {
		_, err := aiWorkServerGoalContext(&cobra.Command{}, c, scope[0], scope[1], scope[2])
		mustFail(err)
	}
	if err := run("context", "--cross-product"); err != nil {
		t.Fatal(err)
	}
	if err := run("bind", "--product", id, "--confirm"); err != nil {
		t.Fatal(err)
	}
	mode = "switch"
	mustFail(run("context", "--feature", other))
	mode = "failure"
	mustFail(run("context"))
	mustFail(run("bind", "--product", id, "--confirm"))
	mustFail(run("start", "--id", other, "--title", "Maintenance", "--maintenance-reason", "Build tooling"))
	mode = "valid"
	file, _, err := aiWorkBindingLocation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"invalid", `{"schema":2}`, strings.Repeat("x", 16385)} {
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		mustFail(run("context"))
		mustFail(run("bind", "--product", id, "--confirm"))
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	mustFail(run("start", "--id", other, "--title", "Maintenance", "--maintenance-reason", "Build tooling"))
	if err := os.Remove(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(file), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	mustFail(run("context"))
	if err := os.Remove(filepath.Dir(file)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", "/nonexistent/foreign/repository")
	if _, err := aiWorkGit(context.Background(), "remote", "remove", "origin"); err != nil {
		t.Fatal(err)
	}
	mustFail(run("context"))
	mustFail(run("bind", "--product", id, "--confirm"))
	t.Chdir(t.TempDir())
	mustFail(run("context"))
	mustFail(run("bind", "--product", id, "--confirm"))
	f.NewClient = func(context.Context) (*client.Client, error) { return nil, fmt.Errorf("login unavailable") }
	mustFail(run("context", "--cross-product"))
	mustFail(run("start", "--id", other, "--title", "Feature", "--feature", id))
}
