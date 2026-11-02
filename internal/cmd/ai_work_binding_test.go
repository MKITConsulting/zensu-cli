package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestAIWorkConfirmedBinding(t *testing.T) {
	aiWorkTestRepo(t)
	const product = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	const org = "891419fa-f7e4-4a52-8123-2bef791876dd"
	user := "42a9be6a-a419-40a5-b24d-64c962f23159"
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/ai-work/capture/context": func(r recordedRequest) fakeReply {
			return fakeReply{Status: 200, Body: map[string]any{"productId": product, "productName": "Confirmed product", "reportingUserId": user, "organizationId": org}}
		},
		"POST /api/ai-work/capture/units": reply(201, map[string]any{"id": org}),
	})
	f, out := testFactory(srv)
	f.NewClient = func(context.Context) (*client.Client, error) {
		return client.New(&config.Config{AccessToken: "personal"}, srv.URL, "", client.WithHTTPClient(srv.Client())), nil
	}
	run := func(args ...string) error { out.Reset(); return runCmd(t, NewAIWorkCmd(f), args...) }
	if err := run("context"); err != nil {
		t.Fatal(err)
	}
	var missing aiWorkContext
	if json.Unmarshal(out.Bytes(), &missing) != nil || missing.Ready || missing.WorkScope != "unassigned" || len(api.calls()) != 0 {
		t.Fatalf("unbound context %s", out.Bytes())
	}
	if run("bind", "--product", product) == nil || len(api.calls()) != 0 {
		t.Fatal("unconfirmed binding reached server")
	}
	if run("start", "--id", org, "--title", "Unbound") == nil || len(api.calls()) != 0 {
		t.Fatal("unbound goal created")
	}
	if err := run("bind", "--product", product, "--confirm"); err != nil {
		t.Fatal(err)
	}
	file, _, err := aiWorkBindingLocation(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var b aiWorkBinding
	if json.Unmarshal(raw, &b) != nil || b.ProductID != product || b.UserID != user || b.OrganizationID != org || b.Repository == "" {
		t.Fatalf("binding %s", raw)
	}
	if info, _ := os.Stat(file); info.Mode().Perm()&0077 != 0 {
		t.Fatal("binding permissions expose identity")
	}
	if err := run("context"); err != nil {
		t.Fatal(err)
	}
	var bound aiWorkContext
	if json.Unmarshal(out.Bytes(), &bound) != nil || !bound.Ready || bound.ProductID != product || bound.UserID != user {
		t.Fatalf("bound context %s", out.Bytes())
	}
	if err := run("start", "--id", org, "--title", "Confirmed", "--maintenance-reason", "Upgrade build tooling", "--source", "codex", "--source-version", "1"); err != nil {
		t.Fatal(err)
	}
	got := api.only(t, "POST", "/api/ai-work/capture/units")
	if got.Body["productId"] != product {
		t.Fatalf("wrong scope: %v", got.Body)
	}
	if run("start", "--id", org, "--title", "Wrong product", "--maintenance-reason", "Upgrade build tooling", "--product", org, "--source", "codex", "--source-version", "1") == nil {
		t.Fatal("plugin overrode the confirmed product")
	}
	api.only(t, "POST", "/api/ai-work/capture/units")
	b.APIURL = "https://other.example.test"
	raw, _ = json.Marshal(b)
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	before := len(api.calls())
	if run("context") == nil || len(api.calls()) != before {
		t.Fatal("changed API host was not rejected before contacting the server")
	}
	if err = run("bind", "--product", product, "--confirm"); err != nil {
		t.Fatal(err)
	}
	b.APIURL = srv.URL
	user = org
	if run("context") == nil {
		t.Fatal("changed account accepted")
	}
	user = b.UserID
	b.OrganizationID = user
	raw, _ = json.Marshal(b)
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if run("context") == nil {
		t.Fatal("changed organization accepted")
	}
	if err := run("bind", "--product", product, "--confirm"); err != nil {
		t.Fatal(err)
	}
	if _, err = aiWorkGit(context.Background(), "remote", "set-url", "origin", "https://example.test/other.git"); err != nil {
		t.Fatal(err)
	}
	if run("context") == nil {
		t.Fatal("changed repository accepted")
	}
	if err = os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(t.TempDir(), "target"), file); err != nil {
		t.Fatal(err)
	}
	if run("bind", "--product", product, "--confirm") == nil {
		t.Fatal("symlink accepted")
	}
}
