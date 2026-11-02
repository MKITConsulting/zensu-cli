package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		{"start", "POST", "/api/ai-work/capture/units", []string{"start", "--id", id, "--title", "Agreed goal", "--work-order", event}, map[string]any{"id": id, "title": "Agreed goal", "workOrderId": event, "source": "cli", "sourceVersion": "dev", "runKind": "work"}},
		{"plugin test", "POST", "/api/ai-work/capture/units", []string{"start", "--id", id, "--title", "Probe", "--source", "copilot", "--source-version", "1.2.3", "--run-kind", "test"}, map[string]any{"id": id, "title": "Probe", "source": "copilot", "sourceVersion": "1.2.3", "runKind": "test"}},
		{"legacy replay", "POST", "/api/ai-work/capture/units", []string{"start", "--id", id, "--title", "Old goal", "--legacy-metadata"}, map[string]any{"id": id, "title": "Old goal"}},
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
	for _, flags := range [][]string{{"--source", "web"}, {"--run-kind", "unknown"}, {"--source-version", "1\x00"}, {"--source", "codex"}, {"--legacy-metadata", "--run-kind", "test"}} {
		f, _ := failingFactory()
		args := append([]string{"start", "--id", id, "--title", "Goal"}, flags...)
		if err := runCmd(t, NewAIWorkCmd(f), args...); err == nil {
			t.Fatalf("invalid metadata accepted: %v", flags)
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

func TestAIWorkReviewFile(t *testing.T) {
	const id = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	const event = "891419fa-f7e4-4a52-8123-2bef791876dd"
	path := "/api/ai-work/capture/units/" + id + "/submissions"
	valid := `{"summary":"Added review context","acceptanceCriteria":[{"text":"Shows criteria","status":"met"}],"evidence":[{"label":"Tests","description":"12 passed"}],"limitations":"Manual review pending"}`
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"valid", valid, true}, {"unknown field", `{"summary":"x","transcript":"private"}`, false},
		{"nested unknown", `{"summary":"x","evidence":[{"label":"Tests","secret":"private"}]}`, false},
		{"missing summary", `{}`, false}, {"null", `null`, false}, {"trailing object", valid + `{}`, false},
		{"malformed", `{`, false}, {"oversize", strings.Repeat(" ", 32769), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "review.json")
			if err := os.WriteFile(file, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST " + path: reply(200, map[string]any{"id": event})})
			f, _ := testFactory(srv)
			f.NewClient = func(context.Context) (*client.Client, error) {
				return client.New(&config.Config{AccessToken: "personal-login"}, srv.URL, "", client.WithHTTPClient(srv.Client())), nil
			}
			err := runCmd(t, NewAIWorkCmd(f), "submit", id, "--event-id", event, "--expected-version", "2", "--artifact", "commit:abc", "--criteria-version", "v1", "--review-file", file)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if !tc.valid {
				if len(api.calls()) != 0 {
					t.Fatal("invalid file sent a request")
				}
				return
			}
			got := api.only(t, "POST", path)
			var want any
			_ = json.Unmarshal([]byte(valid), &want)
			a, _ := json.Marshal(got.Body["reviewDetails"])
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatalf("details=%s want=%s", a, b)
			}
		})
	}
	if _, err := readAIWorkReviewFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	if _, err := readAIWorkReviewFile(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
}
