package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestAIWorkFeatureScope(t *testing.T) {
	aiWorkTestRepo(t)
	const product = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	const feature = "891419fa-f7e4-4a52-8123-2bef791876dd"
	const revision = "42a9be6a-a419-40a5-b24d-64c962f23159"
	spoof := false
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{
		"GET /api/ai-work/capture/context": func(r recordedRequest) fakeReply {
			f := r.Query.Get("featureId")
			if spoof {
				f = revision
			}
			return fakeReply{Status: 200, Body: map[string]any{"productId": product, "productName": "Product", "featureId": f, "featureRevisionId": r.Query.Get("featureRevisionId"), "featureTitle": "Feature", "reportingUserId": revision, "organizationId": product}}
		},
		"POST /api/ai-work/capture/units": reply(201, map[string]any{"id": revision}),
	})
	f, out := testFactory(srv)
	f.NewClient = func(context.Context) (*client.Client, error) {
		return client.New(&config.Config{AccessToken: "personal"}, srv.URL, "", client.WithHTTPClient(srv.Client())), nil
	}
	run := func(args ...string) error { out.Reset(); return runCmd(t, NewAIWorkCmd(f), args...) }
	if err := run("start", "--id", revision, "--title", "Feature goal", "--feature", feature, "--feature-revision", revision); err != nil {
		t.Fatal(err)
	}
	body := api.only(t, "POST", "/api/ai-work/capture/units").Body
	if body["productId"] != product || body["featureId"] != feature || body["featureRevisionId"] != revision || body["maintenanceReason"] != nil {
		t.Fatalf("scope lost: %v", body)
	}
	if err := run("bind", "--product", product, "--confirm"); err != nil {
		t.Fatal(err)
	}
	if err := run("context", "--feature", feature, "--feature-revision", revision); err != nil {
		t.Fatal(err)
	}
	var goal aiWorkContext
	if json.Unmarshal(out.Bytes(), &goal) != nil || !goal.Ready || goal.FeatureID != feature || goal.FeatureRevisionID != revision || goal.ProductID != product {
		t.Fatalf("context: %s", out.Bytes())
	}
	spoof = true
	if run("start", "--id", revision, "--title", "Wrong feature", "--feature", feature) == nil {
		t.Fatal("mismatched feature response accepted")
	}
	api.only(t, "POST", "/api/ai-work/capture/units") // Refusal did not create another unit.
}

func TestAIWorkGoalScopeRefusals(t *testing.T) {
	const id = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	for _, flags := range [][]string{
		{}, {"--product", id}, {"--feature", "invalid"}, {"--feature", id, "--feature-revision", "invalid"},
		{"--feature-revision", id}, {"--feature", id, "--maintenance-reason", "Build"},
		{"--feature", id, "--cross-product-reason", "Shared"}, {"--maintenance-reason", "Build", "--cross-product-reason", "Shared"},
		{"--maintenance-reason", " "}, {"--maintenance-reason", "a\nb"}, {"--maintenance-reason", strings.Repeat("a", 501)},
		{"--legacy-metadata", "--feature", id}, {"--legacy-metadata", "--maintenance-reason", "Build"},
	} {
		f, _ := failingFactory()
		if runCmd(t, NewAIWorkCmd(f), append([]string{"start", "--id", id, "--title", "Goal"}, flags...)...) == nil {
			t.Fatalf("invalid scope accepted: %v", flags)
		}
	}
	for _, args := range [][]string{{"context", "--feature-revision", id}, {"context", "--cross-product", "--feature", id}} {
		f, _ := failingFactory()
		if runCmd(t, NewAIWorkCmd(f), args...) == nil {
			t.Fatalf("invalid context accepted: %v", args)
		}
	}
}
