package cmd

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestAIWorkCommands(t *testing.T) {
	const id = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	const event = "891419fa-f7e4-4a52-8123-2bef791876dd"
	for _, tc := range []struct {
		name, method, path string
		args               []string
		body               map[string]any
	}{
		{"start", "POST", "/api/ai-work/capture/units", []string{"start", "--id", id, "--title", "Agreed goal", "--work-order", event}, map[string]any{"id": id, "title": "Agreed goal", "workOrderId": event}},
		{"get", "GET", "/api/ai-work/capture/units/" + id, []string{"get", id}, nil},
		{"submit", "POST", "/api/ai-work/capture/units/" + id + "/submissions", []string{"submit", id, "--event-id", event, "--expected-version", "2", "--artifact", "commit:abc", "--criteria-version", "sha256:def"}, map[string]any{"id": event, "expectedVersion": float64(2), "artifactRef": "commit:abc", "criteriaVersion": "sha256:def"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{tc.method + " " + tc.path: reply(200, map[string]any{"id": id})})
			f, out := testFactory(srv)
			f.NewClient = func(context.Context) (*client.Client, error) {
				return client.New(&config.Config{AccessToken: "personal-login"}, srv.URL, "", client.WithHTTPClient(srv.Client())), nil
			}
			if err := runCmd(t, NewAIWorkCmd(f), tc.args...); err != nil {
				t.Fatal(err)
			}
			got := api.only(t, tc.method, tc.path)
			want, _ := json.Marshal(tc.body)
			actual, _ := json.Marshal(got.Body)
			if string(want) != string(actual) {
				t.Fatalf("body=%s want=%s", actual, want)
			}
			if got.Auth != "Bearer personal-login" || got.APIKey != "" || !json.Valid(out.Bytes()) {
				t.Fatalf("wrong authentication or output: %v", got)
			}
		})
	}
	for _, args := range [][]string{{"start", "--id", "invalid", "--title", "goal"}, {"start", "--id", id}, {"get", "../other"}, {"submit", id, "--event-id", event}, {"accept", id}, {"revoke", id}, {"start", "--id", id, "--title", "goal", "--user-id", event}} {
		f, _ := failingFactory()
		if runCmd(t, NewAIWorkCmd(f), args...) == nil {
			t.Fatalf("accepted invalid command %v", args)
		}
	}
	for _, cfg := range []*config.Config{{APIKey: "zsk_shared"}, {AccessToken: "zst_worker"}, {}} {
		srv, api := newFakeAPI(t, nil)
		f, _ := testFactory(srv)
		f.NewClient = func(context.Context) (*client.Client, error) { return client.New(cfg, srv.URL, ""), nil }
		if runCmd(t, NewAIWorkCmd(f), "get", id) == nil {
			t.Fatal("credential not refused")
		}
		if len(api.calls()) != 0 {
			t.Fatal("refused credential sent a request")
		}
	}
}

func TestAIWorkContext(t *testing.T) {
	for _, tc := range []struct {
		url     string
		cfg     config.Config
		allowed bool
	}{
		{"https://self-hosted.example/api", config.Config{AccessToken: "login"}, true},
		{"http://localhost:8080", config.Config{AccessToken: "login"}, true},
		{"https://user:secret@example.test", config.Config{AccessToken: "login"}, false},
		{"https://example.test?token=secret", config.Config{AccessToken: "login"}, false},
		{"https://example.test#secret", config.Config{AccessToken: "login"}, false},
		{"file:///private/config", config.Config{AccessToken: "login"}, false},
		{"https://example.test", config.Config{APIKey: "shared"}, false},
		{"https://example.test", config.Config{AccessToken: "zst_worker"}, false},
	} {
		f, out := failingFactory()
		f.NewClient = func(context.Context) (*client.Client, error) { return client.New(&tc.cfg, tc.url, ""), nil }
		err := runCmd(t, NewAIWorkCmd(f), "context")
		if (err == nil) != tc.allowed {
			t.Fatalf("allowed=%t error=%v", tc.allowed, err)
		}
		if tc.allowed {
			var result map[string]string
			if json.Unmarshal(out.Bytes(), &result) != nil || len(result) != 1 || result["apiUrl"] != tc.url {
				t.Fatal("context did not return only the selected host")
			}
		} else if out.Len() != 0 {
			t.Fatal("refused context exposed output")
		}
	}
}
