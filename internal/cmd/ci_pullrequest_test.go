package cmd

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func pullRequestPayload(state string, merged bool) map[string]any {
	return map[string]any{
		"pull_request": map[string]any{
			"number":           17,
			"title":            "[ZEN-42] Rotate refresh tokens",
			"body":             "Closes [ZEN-7] and [ZEN-404].",
			"html_url":         "https://github.com/acme/shop/pull/17",
			"state":            state,
			"merged":           merged,
			"merge_commit_sha": "9f1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c",
			"head":             map[string]any{"sha": "3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b", "ref": "feat/rotate-tokens"},
			"base":             map[string]any{"sha": "0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f8a9b", "ref": "main"},
		},
	}
}

func pullRequestReply(r recordedRequest) fakeReply {
	text, _ := r.Body["title"].(string)
	if body, ok := r.Body["body"].(string); ok {
		text += " " + body
	}
	linked, unresolved := []map[string]any{}, []string{}
	for _, m := range featureReferencePattern.FindAllStringSubmatch(text, -1) {
		if m[2] == "42" || m[2] == "7" {
			linked = append(linked, map[string]any{"slug": m[2], "ref": strings.ToUpper(m[1]) + "-" + m[2], "featureId": "f-" + m[2]})
		} else {
			unresolved = append(unresolved, m[2])
		}
	}
	return fakeReply{Body: map[string]any{"prNumber": r.Body["prNumber"], "linked": linked, "unresolved": unresolved, "workOrderMergeRecorded": false}}
}

func pullRequestAPI(t *testing.T, handler func(recordedRequest) fakeReply) (*Factory, *fakeAPI) {
	t.Helper()
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{"POST /api/ci/pull-requests": handler})
	f, _ := testFactory(srv)
	return f, api
}

func TestCIPullRequest_UploadsThePullRequestOfTheEvent(t *testing.T) {
	clearGitHubEnv(t)
	t.Setenv("GITHUB_SERVER_URL", "https://github.com/")
	t.Setenv("GITHUB_REPOSITORY", "acme/shop")
	for _, tc := range []struct {
		state  string
		merged bool
		status string
	}{
		{"open", false, "open"},
		{"closed", true, "merged"},
		{"closed", false, "closed"},
	} {
		t.Run(tc.status, func(t *testing.T) {
			githubEvent(t, "pull_request", pullRequestPayload(tc.state, tc.merged))
			f, api := pullRequestAPI(t, pullRequestReply)
			res := runCI(t, f, "", "pull-request", "--github", "--product", ciTestProduct)
			if res.err != nil {
				t.Fatalf("pull-request: %v\n%s", res.err, res.stderr)
			}
			mergeSha := ""
			if tc.merged {
				mergeSha = `, "mergeSha": "9f1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c"`
			}
			call := api.only(t, http.MethodPost, "/api/ci/pull-requests")
			requireJSONEqual(t, call.Raw, fmt.Sprintf(`{
				"productId": %q,
				"prNumber": 17,
				"title": "[ZEN-42] Rotate refresh tokens",
				"body": "Closes [ZEN-7] and [ZEN-404].",
				"prUrl": "https://github.com/acme/shop/pull/17",
				"status": %q,
				"repository": "https://github.com/acme/shop",
				"baseRef": "main",
				"headSha": "3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b"%s
			}`, ciTestProduct, tc.status, mergeSha))
			if res.stdout != "Zensu linked pull request #17 to ZEN-42, ZEN-7.\nNo feature of this product matches ZEN-404.\n" || res.stderr != "" {
				t.Fatalf("stdout = %q stderr = %q", res.stdout, res.stderr)
			}
		})
	}
}

func TestCIPullRequest_ReportsWhichReferencesMatch(t *testing.T) {
	clearGitHubEnv(t)
	for _, tc := range []struct {
		name   string
		title  string
		body   any
		output string
	}{
		{"no reference", "Rotate refresh tokens", nil, "Pull request #17 references no feature of this product.\n"},
		{"every reference resolves", "[ZEN-42] Rotate refresh tokens", "Closes [ZEN-7].", "Zensu linked pull request #17 to ZEN-42, ZEN-7.\n"},
		{"no reference resolves", "[zen-404] Rotate [ZEN-404] and [ZEN-500]", "", "Pull request #17 references no feature of this product.\nNo feature of this product matches ZEN-404.\nNo feature of this product matches ZEN-500.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := pullRequestPayload("open", false)
			pr := payload["pull_request"].(map[string]any)
			pr["title"], pr["body"] = tc.title, tc.body
			githubEvent(t, "pull_request", payload)
			f, api := pullRequestAPI(t, pullRequestReply)
			res := runCI(t, f, "", "pull-request", "--github", "--product", ciTestProduct)
			if res.err != nil {
				t.Fatalf("pull-request: %v", res.err)
			}
			if _, hasBody := api.only(t, http.MethodPost, "/api/ci/pull-requests").Body["body"]; hasBody != (tc.body != nil && tc.body != "") {
				t.Fatalf("body sent = %v for body %#v", hasBody, tc.body)
			}
			if res.stdout != tc.output {
				t.Fatalf("stdout = %q, want %q", res.stdout, tc.output)
			}
		})
	}
}

func TestCIPullRequest_FlagsDescribeThePullRequestWithoutGitHub(t *testing.T) {
	clearGitHubEnv(t)
	body := writeTestFile(t, "body.md", "Closes [ZEN-7].\n\n```sh\necho \"$HOME\" `date`\n```\n")
	f, api := pullRequestAPI(t, pullRequestReply)
	res := runCI(t, f, "", "pull-request", "--product", ciTestProduct, "--number", "5", "--title", "[ZEN-42] Rotate",
		"--url", "https://git.example.test/acme/shop/-/merge_requests/5", "--status", "merged", "--body-file", body,
		"--repository", "https://git.example.test/acme/shop", "--base-ref", "main", "--head-sha", "abc123", "--merge-sha", "def456")
	if res.err != nil {
		t.Fatalf("pull-request: %v", res.err)
	}
	requireJSONEqual(t, api.only(t, http.MethodPost, "/api/ci/pull-requests").Raw, `{
		"productId": "`+ciTestProduct+`",
		"prNumber": 5,
		"title": "[ZEN-42] Rotate",
		"body": "Closes [ZEN-7].\n\n`+"```"+`sh\necho \"$HOME\" `+"`date`"+`\n`+"```"+`",
		"prUrl": "https://git.example.test/acme/shop/-/merge_requests/5",
		"status": "merged",
		"repository": "https://git.example.test/acme/shop",
		"baseRef": "main",
		"headSha": "abc123",
		"mergeSha": "def456"
	}`)
	if res.stdout != "Zensu linked pull request #5 to ZEN-42, ZEN-7.\n" {
		t.Fatalf("stdout = %q", res.stdout)
	}
}

func TestCIPullRequest_ExplicitFlagsWinOverTheEvent(t *testing.T) {
	clearGitHubEnv(t)
	t.Setenv("GITHUB_SERVER_URL", "https://github.com")
	t.Setenv("GITHUB_REPOSITORY", "acme/shop")
	githubEvent(t, "pull_request", pullRequestPayload("open", false))
	f, api := pullRequestAPI(t, pullRequestReply)
	res := runCI(t, f, "", "pull-request", "--github", "--product", ciTestProduct, "--title", "[ZEN-7] Renamed", "--body", "", "--status", "closed", "--repository", "https://mirror.example.test/shop")
	if res.err != nil {
		t.Fatalf("pull-request: %v", res.err)
	}
	call := api.only(t, http.MethodPost, "/api/ci/pull-requests")
	if call.Body["title"] != "[ZEN-7] Renamed" || call.Body["status"] != "closed" || call.Body["repository"] != "https://mirror.example.test/shop" || call.Body["prNumber"] != float64(17) {
		t.Fatalf("body = %v", call.Body)
	}
	if _, ok := call.Body["body"]; ok {
		t.Fatalf("an empty --body still sent the event's body: %v", call.Body)
	}
}

func TestCIPullRequest_RefusesIncompleteInputBeforeUploading(t *testing.T) {
	clearGitHubEnv(t)
	for _, tc := range []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"no number", []string{"--title", "t"}, "", "--number is required unless --github reads a pull_request event"},
		{"no title", []string{"--number", "3"}, "", "--title is required unless --github reads a pull_request event"},
		{"blank title", []string{"--number", "3", "--title", "  "}, "", "--title is required unless --github reads a pull_request event"},
		{"negative number", []string{"--number", "-3", "--title", "t"}, "", "pull request number -3 is out of range"},
		{"huge number", []string{"--number", "2147483648", "--title", "t"}, "", "pull request number 2147483648 is out of range"},
		{"unknown status", []string{"--number", "3", "--title", "t", "--status", "draft"}, "", `--status must be open, closed or merged, got "draft"`},
		{"github without a pull_request event", []string{"--github"}, "", "--number is required unless --github reads a pull_request event"},
		{"two readers of stdin", []string{"--number", "3", "--title", "t", "--api-key-stdin", "--api-url", "http://127.0.0.1:1", "--body-file", "-"}, "zsk_EXAMPLE", "--api-key-stdin and --body-file - both read stdin; pass --body-file a file path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "github without a pull_request event" {
				githubEvent(t, "push", map[string]any{"before": "abc"})
			}
			f, api := pullRequestAPI(t, pullRequestReply)
			res := runCI(t, f, tc.stdin, append([]string{"pull-request", "--product", ciTestProduct}, tc.args...)...)
			if res.err == nil || res.err.Error() != tc.want {
				t.Fatalf("error = %v, want %q", res.err, tc.want)
			}
			if n := len(api.calls()); n != 0 {
				t.Fatalf("%d requests, want none", n)
			}
		})
	}
}

func TestCIPullRequest_WarnsWhenMergeEvidenceWasNotRecorded(t *testing.T) {
	clearGitHubEnv(t)
	reply := func(recordedRequest) fakeReply {
		return fakeReply{Body: `{"prNumber":17,"linked":[],"unresolved":[],"workOrderMergeRecorded":false,"workOrderMergeError":"merge_evidence_needs_ci_key"}`}
	}
	for _, tc := range []struct {
		args   []string
		stderr string
	}{
		{nil, "warning: Zensu kept pull request #17 but did not record its merge evidence: merge_evidence_needs_ci_key\n"},
		{[]string{"--github"}, "::warning::Zensu kept pull request #17 but did not record its merge evidence: merge_evidence_needs_ci_key\n"},
		{[]string{"--json"}, "warning: Zensu kept pull request #17 but did not record its merge evidence: merge_evidence_needs_ci_key\n"},
	} {
		f, _ := pullRequestAPI(t, reply)
		res := runCI(t, f, "", append([]string{"pull-request", "--product", ciTestProduct, "--number", "17", "--title", "Rotate", "--status", "merged"}, tc.args...)...)
		if res.err != nil || res.stderr != tc.stderr {
			t.Errorf("%v: err %v stderr %q", tc.args, res.err, res.stderr)
		}
	}
}

func TestCIPullRequest_JSONPrintsTheAnswer(t *testing.T) {
	clearGitHubEnv(t)
	f, _ := pullRequestAPI(t, pullRequestReply)
	res := runCI(t, f, "", "pull-request", "--json", "--product", ciTestProduct, "--number", "17", "--title", "[ZEN-42] Rotate")
	if res.err != nil {
		t.Fatalf("pull-request: %v", res.err)
	}
	requireJSONEqual(t, res.stdout, `{"prNumber":17,"linked":[{"slug":"42","ref":"ZEN-42","featureId":"f-42"}],"unresolved":[],"workOrderMergeRecorded":false}`)
}
