package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const validPlanFile = `---
product: 11111111-2222-4333-8444-555555555555
repository: https://github.com/acme/app
base_branch: develop
integration_branch: plan/checkout
harness: claude
items:
  - feature: ZEN-42
    requirements: [AC-001, {id: AC-002, tags: [payment, auth]}]
    paths: [backend/internal/billing/refunds.go]
  - feature: ZEN-7
    new_revision: true
    scope_summary: Refund partial captures
    requirements:
      - FR-001
  - title: Refund webhook
    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb
    slug: refund-webhook
    scope_summary: Accept refund webhooks
    requirements:
      - id: IF-001
        text: POST /webhooks/refund accepts a signed payload
---

# Checkout hardening

Intro text.

## Requirements

| ID | Requirement | Source |
| --- | --- | --- |
| AC-001 | A captured payment can be refunded | spec |
| ` + "`AC-002`" + ` | Refunds need the payment scope | spec |
| FR-001 | Partial captures refund proportionally | finance |

## Notes

| Name | Value |
| --- | --- |
| free | table |
`

const planFront = "---\nrepository: github.com/acme/app\nname: P\nitems:\n"

func TestParsePlanFile_Valid(t *testing.T) {
	p, err := parsePlanFile([]byte(validPlanFile))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	sum := sha256.Sum256([]byte(validPlanFile))
	if p.contentHash != hex.EncodeToString(sum[:]) {
		t.Fatalf("content hash = %s", p.contentHash)
	}
	if p.Name != "Checkout hardening" || p.Repository != "https://github.com/acme/app" || p.repository != "github.com/acme/app" || p.BaseBranch != "develop" || p.IntegrationBranch != "plan/checkout" || p.Harness != "claude" || p.Task != "" {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.Items) != 3 {
		t.Fatalf("items = %d", len(p.Items))
	}
	first := p.Items[0]
	if first.Feature != "ZEN-42" || len(first.Requirements) != 2 || first.Requirements[0].ID != "AC-001" || first.Requirements[1].ID != "AC-002" || strings.Join(first.Requirements[1].Tags, ",") != "payment,auth" {
		t.Fatalf("first item = %+v", first)
	}
	if first.Paths[0] != "backend/internal/billing/refunds.go" {
		t.Fatalf("paths = %v", first.Paths)
	}
	if got := p.requirements; len(got) != 3 || got["AC-002"] != "Refunds need the payment scope" || got["FR-001"] != "Partial captures refund proportionally" {
		t.Fatalf("requirements table = %v", got)
	}
	if got := p.acceptanceCriteria(p.Items[1]); len(got) != 1 || got[0] != "FR-001: Partial captures refund proportionally" {
		t.Fatalf("acceptance criteria = %v", got)
	}
	if got := p.acceptanceCriteria(p.Items[2]); got[0] != "IF-001: POST /webhooks/refund accepts a signed payload" {
		t.Fatalf("front-matter text must win: %v", got)
	}
	desc := p.featureDescription(p.Items[2])
	if desc != "Accept refund webhooks\n\nAcceptance criteria:\n- IF-001: POST /webhooks/refund accepts a signed payload" {
		t.Fatalf("description = %q", desc)
	}
	payload := requirementPayload(first)
	if len(payload) != 2 || payload[0]["id"] != "AC-001" || len(payload[0]["tags"].([]string)) != 0 || len(payload[1]["tags"].([]string)) != 2 {
		t.Fatalf("requirement payload = %v", payload)
	}
}

func TestParsePlanFile_CRLFAndFrontMatterAtEOF(t *testing.T) {
	crlf := strings.ReplaceAll("---\nrepository: https://github.com/acme/app\nname: Plan\nitems:\n  - feature: ZEN-1\n---\n", "\n", "\r\n")
	p, err := parsePlanFile([]byte(crlf))
	if err != nil {
		t.Fatalf("crlf: %v", err)
	}
	if p.Name != "Plan" || p.Items[0].Feature != "ZEN-1" {
		t.Fatalf("plan = %+v", p)
	}
	eof := "---\nrepository: github.com/acme/app\nname: Plan\nitems:\n  - feature: ZEN-1\n---"
	if _, err := parsePlanFile([]byte(eof)); err != nil {
		t.Fatalf("front matter at EOF: %v", err)
	}
}

func TestParsePlanFile_LineEndingsAndByteOrderMarkDoNotChangeThePlan(t *testing.T) {
	lf, err := parsePlanFile([]byte(validPlanFile))
	if err != nil {
		t.Fatalf("lf: %v", err)
	}
	crlf := strings.ReplaceAll(validPlanFile, "\n", "\r\n")
	variants := map[string]string{
		"crlf":       crlf,
		"bom":        "\xef\xbb\xbf" + validPlanFile,
		"bom crlf":   "\xef\xbb\xbf" + crlf,
		"mixed ends": strings.Replace(validPlanFile, "\n", "\r\n", 5),
	}
	for name, content := range variants {
		p, err := parsePlanFile([]byte(content))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.contentHash != hashOf(validPlanFile) {
			t.Errorf("%s: content hash %s, want the hash of the LF text %s", name, p.contentHash, hashOf(validPlanFile))
		}
		if !reflect.DeepEqual(p, lf) {
			t.Errorf("%s parses differently:\n%+v\n%+v", name, p, lf)
		}
	}
}

func TestParsePlanFile_EmptyDescriptionWithoutScopeOrRequirements(t *testing.T) {
	p, err := parsePlanFile([]byte(planFront + "  - title: New thing\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d := p.featureDescription(p.Items[0]); d != "" {
		t.Fatalf("description = %q", d)
	}
	p2, err := parsePlanFile([]byte(planFront + "  - title: New thing\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n    requirements: [{id: AC-001, text: Works}]\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d := p2.featureDescription(p2.Items[0]); d != "Acceptance criteria:\n- AC-001: Works" {
		t.Fatalf("description = %q", d)
	}
}

func TestParsePlanFile_Errors(t *testing.T) {
	head := "---\nrepository: https://github.com/acme/app\nname: Plan\n"
	cases := map[string]string{
		"no front matter":             "# Plan\n",
		"not closed":                  "---\nrepository: github.com/acme/app\n",
		"unknown key":                 head + "itmes: []\n---\n",
		"yaml syntax":                 head + "items: [\n---\n",
		"no repository":               "---\nname: P\nitems:\n  - feature: ZEN-1\n---\n",
		"no name":                     "---\nrepository: github.com/acme/app\nitems:\n  - feature: ZEN-1\n---\n",
		"no items":                    head + "items: []\n---\n",
		"two kinds":                   head + "items:\n  - feature: ZEN-1\n    title: T\n---\n",
		"no kind":                     head + "items:\n  - paths: [a]\n---\n",
		"bad revision":                head + "items:\n  - revision: nope\n---\n",
		"new feature needs component": head + "items:\n  - title: T\n---\n",
		"component on feature":        head + "items:\n  - feature: ZEN-1\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n---\n",
		"new revision on title":       head + "items:\n  - revision: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n    new_revision: true\n---\n",
		"new revision needs scope":    head + "items:\n  - feature: ZEN-1\n    new_revision: true\n---\n",
		"bad requirement":             head + "items:\n  - feature: ZEN-1\n    requirements: [AC-1]\n---\n",
		"duplicate across items":      head + "items:\n  - feature: ZEN-1\n    requirements: [AC-001]\n  - feature: ZEN-2\n    requirements: [AC-001]\n---\n",
		"bad tag":                     head + "items:\n  - feature: ZEN-1\n    requirements: [{id: AC-001, tags: [Auth]}]\n---\n",
		"too many tags":               head + "items:\n  - feature: ZEN-1\n    requirements: [{id: AC-001, tags: [a,b,c,d,e,f,g,h,i,j,k]}]\n---\n",
		"tags not a list":             head + "items:\n  - feature: ZEN-1\n    requirements: [{id: AC-001, tags: payment}]\n---\n",
		"missing text":                head + "items:\n  - feature: ZEN-1\n    new_revision: true\n    scope_summary: S\n    requirements: [AC-009]\n---\n",
		"table bad id":                head + "items:\n  - feature: ZEN-1\n---\n## Requirements\n| ID | Requirement |\n| --- | --- |\n| AC-1 | x |\n",
		"table duplicate":             head + "items:\n  - feature: ZEN-1\n---\n## Requirements\n| AC-001 | x |\n| AC-001 | y |\n",
		"table empty text":            head + "items:\n  - feature: ZEN-1\n---\n## Requirements\n| AC-001 |  |\n",
		"too large":                   head + strings.Repeat("#", maxPlanFileBytes),
		"long name":                   "---\nrepository: github.com/acme/app\nname: " + strings.Repeat("n", 256) + "\nitems:\n  - feature: ZEN-1\n---\n",
		"bad harness":                 head + "harness: Claude Code\nitems:\n  - feature: ZEN-1\n---\n",
		"bad task":                    head + "task: nope\nitems:\n  - feature: ZEN-1\n---\n",
		"bad product":                 head + "product: acme\nitems:\n  - feature: ZEN-1\n---\n",
		"bad feature":                 head + "items:\n  - feature: checkout\n---\n",
		"bad feature uuid":            head + "items:\n  - feature: 11111111-2222-4333-8444-55555555555\n---\n",
		"bad repository":              "---\nrepository: acme\nname: P\nitems:\n  - feature: ZEN-1\n---\n",
		"repository with credentials": "---\nrepository: https://bot:token@github.com/acme/app\nname: P\nitems:\n  - feature: ZEN-1\n---\n",
		"bad base branch":             head + "base_branch: main..x\nitems:\n  - feature: ZEN-1\n---\n",
		"one-item integration":        head + "integration_branch: plan/x\nitems:\n  - feature: ZEN-1\n---\n",
		"integration without plan/":   head + "integration_branch: feature/x\nitems:\n  - feature: ZEN-1\n  - feature: ZEN-2\n---\n",
		"integration equals base":     head + "base_branch: plan/x\nintegration_branch: plan/x\nitems:\n  - feature: ZEN-1\n  - feature: ZEN-2\n---\n",
		"integration lock suffix":     head + "integration_branch: plan/x.lock\nitems:\n  - feature: ZEN-1\n  - feature: ZEN-2\n---\n",
		"glob path":                   head + "items:\n  - feature: ZEN-1\n    paths: [backend/**]\n---\n",
		"long title":                  head + "items:\n  - title: " + strings.Repeat("t", 501) + "\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n---\n",
		"bad slug":                    head + "items:\n  - title: T\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n    slug: Bad Slug\n---\n",
		"title without slug":          head + "items:\n  - title: '!!!'\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n---\n",
		"same feature twice":          head + "items:\n  - feature: ZEN-1\n  - feature: zen-1\n---\n",
		"same slug twice":             head + "items:\n  - title: Refund webhook\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n  - title: Refund Webhook!\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n---\n",
		"same revision twice":         head + "items:\n  - revision: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n  - revision: 77777777-8888-4999-8AAA-BBBBBBBBBBBB\n---\n",
		"scope on a revision":         head + "items:\n  - revision: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n    scope_summary: S\n---\n",
		"scope on a feature":          head + "items:\n  - feature: ZEN-1\n    scope_summary: S\n---\n",
		"task with several items":     head + "task: 66666666-7777-4888-8999-000000000000\nitems:\n  - feature: ZEN-1\n  - feature: ZEN-2\n---\n",
	}
	want := map[string]string{
		"no front matter":             "must start with a YAML front-matter block",
		"not closed":                  "not closed",
		"unknown key":                 "front matter:",
		"yaml syntax":                 "front matter:",
		"no repository":               "repository is required",
		"no name":                     "name is required",
		"no items":                    "between 1 and 50",
		"two kinds":                   "item 1: name exactly one of feature, revision or title",
		"no kind":                     "item 1: name exactly one",
		"bad revision":                "revision must be a revision UUID",
		"new feature needs component": "a new feature needs component",
		"component on feature":        "component and slug belong to new features",
		"new revision on title":       "new_revision needs feature",
		"new revision needs scope":    "new_revision needs scope_summary",
		"bad requirement":             `"AC-1" is not a requirement ID`,
		"duplicate across items":      "requirement AC-001 appears twice in the plan (items 1 and 2)",
		"bad tag":                     `tag "Auth" of AC-001 must be a lowercase word`,
		"too many tags":               "more than 10 tags",
		"tags not a list":             "front matter: yaml: unmarshal errors:\n  line 5: cannot unmarshal !!str `payment` into []string",
		"missing text":                "AC-009 has no text",
		"table bad id":                `"AC-1" is not a requirement ID`,
		"table duplicate":             "duplicate requirement ID AC-001",
		"table empty text":            "AC-001 has no requirement text",
		"too large":                   "exceeds",
		"long name":                   "name must be at most 255 bytes",
		"bad harness":                 "harness must be a short lowercase name",
		"bad task":                    "task must be a task UUID",
		"bad product":                 "front matter: product must be a product UUID",
		"bad feature":                 "item 1: feature must be a feature key like ZEN-42 or a feature UUID",
		"bad feature uuid":            "item 1: feature must be a feature key like ZEN-42 or a feature UUID",
		"bad repository":              "front matter: repository must have the form host/owner/name",
		"repository with credentials": "front matter: repository must not carry credentials",
		"bad base branch":             "base_branch is not a valid branch name",
		"one-item integration":        "a one-item plan integrates into its base branch",
		"integration without plan/":   "integration_branch of a plan with several items must be a branch name starting with plan/",
		"integration equals base":     "integration_branch of a plan with several items must be a branch name starting with plan/",
		"integration lock suffix":     "integration_branch of a plan with several items must be a branch name starting with plan/",
		"glob path":                   `item 1: path "backend/**" must be a clean relative file path without wildcards`,
		"long title":                  "item 1: title must be at most 500 characters",
		"bad slug":                    "item 1: slug must be up to 200 lowercase letters",
		"title without slug":          "item 1: slug must be up to 200 lowercase letters",
		"same feature twice":          "items 1 and 2 plan the same feature",
		"same slug twice":             "items 1 and 2 plan the same feature",
		"same revision twice":         "items 1 and 2 plan the same feature",
		"scope on a revision":         "item 1: scope_summary belongs to new features (title) and new revisions (feature with new_revision: true); an existing revision keeps its scope",
		"scope on a feature":          "item 1: scope_summary belongs to new features (title) and new revisions (feature with new_revision: true); an existing revision keeps its scope",
		"task with several items":     "front matter: task links the work order of a one-item plan, and this plan has 2 items; drop task or push the item that implements the task as a plan of its own",
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parsePlanFile([]byte(file))
			if err == nil || !strings.Contains(err.Error(), want[name]) {
				t.Fatalf("error = %v, want %q", err, want[name])
			}
		})
	}
}

func TestParsePlanFile_NamesAnUnknownRequirementKey(t *testing.T) {
	file := planFront + "  - title: Refund webhook\n    component: 77777777-8888-4999-8aaa-bbbbbbbbbbbb\n    requirements: [{id: AC-003, tag: [payment]}]\n---\n"
	_, err := parsePlanFile([]byte(file))
	if err == nil || err.Error() != `front matter: line 6: unknown requirement key "tag"; a requirement takes id, text and tags` {
		t.Fatalf("error = %v", err)
	}
	block := planFront + "  - feature: ZEN-1\n    requirements:\n      - id: AC-001\n        text: Works\n        tags: [payment]\n        owner: finance\n---\n"
	if _, err := parsePlanFile([]byte(block)); err == nil || err.Error() != `front matter: line 9: unknown requirement key "owner"; a requirement takes id, text and tags` {
		t.Fatalf("error = %v", err)
	}
	p, err := parsePlanFile([]byte(planFront + "  - feature: ZEN-1\n    requirements: [{id: ' AC-001 ', text: Works, tags: [payment]}]\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if r := p.Items[0].Requirements[0]; r.ID != "AC-001" || r.Text != "Works" || strings.Join(r.Tags, ",") != "payment" {
		t.Fatalf("requirement = %+v", r)
	}
}

func TestParsePlanFile_AcceptsFeatureKeysAndUUIDs(t *testing.T) {
	p, err := parsePlanFile([]byte(planFront + "  - feature: zen-42\n  - feature: 11111111-2222-4333-8444-555555555555\n  - feature: ' ZEN-7 '\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Items) != 3 || planFeatureRef(p.Items[0].Feature) != "ZEN-42" || planFeatureRef(p.Items[1].Feature) != "11111111-2222-4333-8444-555555555555" || planFeatureRef(p.Items[2].Feature) != "ZEN-7" {
		t.Fatalf("items = %+v", p.Items)
	}
}

func TestNormalizePlanRepository(t *testing.T) {
	valid := map[string]string{
		"github.com/acme/app":                      "github.com/acme/app",
		" https://github.com/Acme/App.git ":        "github.com/acme/app",
		"HTTPS://github.com/acme/app/":             "github.com/acme/app",
		"http://gitlab.example.com/group/sub/app/": "gitlab.example.com/group/sub/app",
		"ssh://git@github.com:22/acme/app.git":     "github.com/acme/app",
		"ssh://github.com/acme/app":                "github.com/acme/app",
		"git@github.com:acme/app.git":              "github.com/acme/app",
		"git.example.com:8443/acme/app":            "git.example.com:8443/acme/app",
	}
	for in, want := range valid {
		if got, err := normalizePlanRepository(in); err != nil || got != want {
			t.Errorf("normalizePlanRepository(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	form := "front matter: repository must have the form host/owner/name"
	invalid := map[string]string{
		"https://bot:token@github.com/acme/app": "front matter: repository must not carry credentials",
		"acme":                                  form,
		"/acme/app":                             form,
		"github.com/acme":                       form,
		"ssh://github.com":                      form,
		"github.com/acme/../app":                form,
		"github.com/acme/app name":              form,
		"github.com/" + strings.Repeat("a", 250) + "/b": form,
	}
	for in, want := range invalid {
		if got, err := normalizePlanRepository(in); err == nil || err.Error() != want {
			t.Errorf("normalizePlanRepository(%q) = %q, %v, want %s", in, got, err, want)
		}
	}
}

func TestParsePlanFile_TooManyItems(t *testing.T) {
	var b strings.Builder
	b.WriteString(planFront)
	for i := 0; i < 51; i++ {
		b.WriteString("  - feature: ZEN-1\n")
	}
	b.WriteString("---\n")
	if _, err := parsePlanFile([]byte(b.String())); err == nil || !strings.Contains(err.Error(), "between 1 and 50") {
		t.Fatalf("error = %v", err)
	}
}

func TestParsePlanFile_Limits(t *testing.T) {
	item := func(feature int, paths, reqs []string) string {
		s := fmt.Sprintf("  - feature: ZEN-%d\n", feature)
		if len(paths) > 0 {
			s += "    paths: [" + strings.Join(paths, ", ") + "]\n"
		}
		if len(reqs) > 0 {
			s += "    requirements: [" + strings.Join(reqs, ", ") + "]\n"
		}
		return s
	}
	seq := func(format string, from, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf(format, from+i)
		}
		return out
	}
	plan := func(items ...string) string {
		return planFront + strings.Join(items, "") + "---\n"
	}
	manyPaths := make([]string, 0, 11)
	for i := 0; i < 11; i++ {
		manyPaths = append(manyPaths, item(i+1, seq("f%d.go", i*100, 100), nil))
	}
	manyReqs := make([]string, 0, 6)
	for i := 0; i < 6; i++ {
		manyReqs = append(manyReqs, item(i+1, nil, seq("AC-%04d", i*200, 200)))
	}
	cases := []struct {
		name string
		file string
		want string
	}{
		{name: "paths of one item", file: plan(item(1, seq("f%d.go", 0, 101), nil)), want: "item 1: at most 100 paths per item"},
		{name: "paths of the plan", file: plan(manyPaths...), want: "a plan names at most 1000 paths in total"},
		{name: "requirements of one item", file: plan(item(1, nil, seq("AC-%04d", 0, 201))), want: "item 1: at most 200 requirements per item"},
		{name: "requirements of the plan", file: plan(manyReqs...), want: "a plan holds at most 1000 requirements in total"},
		{name: "as many paths and requirements as allowed", file: plan(item(1, seq("f%d.go", 0, 100), seq("AC-%04d", 0, 200)))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parsePlanFile([]byte(tc.file))
			if tc.want == "" {
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParsePlanFile_OneItemPlanMayNameItsBaseAsIntegrationBranch(t *testing.T) {
	p, err := parsePlanFile([]byte("---\nrepository: github.com/acme/app\nname: P\nbase_branch: develop\nintegration_branch: develop\nitems:\n  - feature: ZEN-1\n    paths: [docs/plans/refunds.md, \"src/app (copy)/main.go\"]\n---\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.IntegrationBranch != "develop" || len(p.Items[0].Paths) != 2 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestValidPlanPath(t *testing.T) {
	cases := map[string]bool{
		"a.go":                       true,
		"backend/internal/refund.go": true,
		"src/app (copy)/main.go":     true,
		"docs/ümlaut/ä.md":           true,
		"":                           false,
		".":                          false,
		"./a.go":                     false,
		"a/":                         false,
		"a//b":                       false,
		"/etc/passwd":                false,
		`\share\a.go`:                false,
		`a\b.go`:                     false,
		"a*.go":                      false,
		"a?.go":                      false,
		"C:a.go":                     false,
		"..":                         false,
		"../a.go":                    false,
		" a.go":                      false,
		"a\x01.go":                   false,
		"a\nb.go":                    false,
		"a#b.go":                     false,
		"a;b.go":                     false,
		strings.Repeat("a", 513):     false,
		strings.Repeat("a", 512):     true,
	}
	for p, want := range cases {
		if got := validPlanPath(p); got != want {
			t.Errorf("validPlanPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestValidPlanBranch(t *testing.T) {
	cases := map[string]bool{
		"main":          true,
		"plan/refunds":  true,
		"release/1.2.x": true,
		"-main":         false,
		"a..b":          false,
		"a//b":          false,
		"a/":            false,
		"a.":            false,
		"a/.hidden":     false,
		"a/b.lock":      false,
		"a b":           false,
	}
	for ref, want := range cases {
		if got := validPlanBranch(ref); got != want {
			t.Errorf("validPlanBranch(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestParseRequirementsTable_IgnoresOtherSectionsAndHeaderlessTables(t *testing.T) {
	body := "## Scope\n| AC-999 | not a requirement section |\n### Requirements\n| AC-001 | first |\n| --- | --- |\n| **FR-002** | second |\n"
	got, err := parseRequirementsTable([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got["AC-001"] != "first" || got["FR-002"] != "second" {
		t.Fatalf("table = %v", got)
	}
}

func TestParseRequirementsTable_FollowsMarkdownStructure(t *testing.T) {
	cases := []struct {
		name string
		body string
		want map[string]string
	}{
		{
			name: "escaped pipes stay in the text",
			body: "## Requirements\n| ID | Requirement |\n| --- | --- |\n| AC-001 | Accept `a \\| b` and c\\|d |\n| AC-002 | Keep \\* and \\\\ as written |\n",
			want: map[string]string{"AC-001": "Accept `a | b` and c|d", "AC-002": "Keep \\* and \\\\ as written"},
		},
		{
			name: "sub-headings stay inside the section",
			body: "## Requirements\n### Payments\n| ID | Requirement |\n| --- | --- |\n| AC-001 | Pay |\n#### Refunds\n| a single cell |\n| AC-002 | Refund |\n## Notes\n| AC-999 | not a requirement |\n",
			want: map[string]string{"AC-001": "Pay", "AC-002": "Refund"},
		},
		{
			name: "a higher heading ends the section",
			body: "### Requirements\n| AC-001 | Pay |\n# Appendix\n| AC-002 | not a requirement |\n",
			want: map[string]string{"AC-001": "Pay"},
		},
		{
			name: "a second table after a blank line has its own header",
			body: "## Requirements\n| ID | Requirement |\n| --- | --- |\n| AC-001 | Pay |\n\n| Key | Text |\n| --- | --- |\n| FR-001 | Log |\nA paragraph\n| Key | Text |\n| IF-001 | Call |\n",
			want: map[string]string{"AC-001": "Pay", "FR-001": "Log", "IF-001": "Call"},
		},
		{
			name: "fenced blocks are skipped",
			body: "## Requirements\n```markdown\n# Not a heading\n| AC-900 | example |\n```\n| AC-001 | Pay |\n~~~~\n## Notes\n| AC-901 | example |\n~~~\n~~~~~\n| AC-002 | Refund |\n   ```\n| AC-902 | unclosed example |\n",
			want: map[string]string{"AC-001": "Pay", "AC-002": "Refund"},
		},
		{
			name: "lines that only look like headings or fences",
			body: "## Requirements ##\n#hashtag\n| AC-001 | Pay |\n    # indented code\n| AC-002 | Refund |\n``` `inline` ```\n| AC-003 | Close |\n    ```\n| AC-004 | Keep |\n####### seven\n| AC-005 | Deep |\n",
			want: map[string]string{"AC-001": "Pay", "AC-002": "Refund", "AC-003": "Close", "AC-004": "Keep", "AC-005": "Deep"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseRequirementsTable([]byte(tc.body))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("table = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitTableRow(t *testing.T) {
	cases := map[string][]string{
		"| a | b |":      {" a ", " b "},
		"| a | b":        {" a ", " b"},
		"a | b |":        {"a ", " b "},
		`| a \| b | c |`: {" a | b ", " c "},
		`| a | b \|`:     {" a ", " b |"},
		`| a\\| b |`:     {` a\\`, " b "},
		`| a \`:          {` a \`},
	}
	for row, want := range cases {
		if got := splitTableRow(row); !reflect.DeepEqual(got, want) {
			t.Errorf("splitTableRow(%q) = %q, want %q", row, got, want)
		}
	}
}

func TestFirstHeading(t *testing.T) {
	cases := map[string]string{
		"text\n## Second\n#  Title \n":                     "Title",
		"no heading":                                       "",
		"```\n# Example heading\n```\n# Real title #\n":    "Real title",
		"~~~\n# Example\n~~~\n#\n# C#\n":                   "C#",
		"    # indented code\n#hashtag\n# Title\n":         "Title",
		"```md\n# Only inside an unclosed fence\n":         "",
		"## Second\n\t# tab indented\n  # Two spaces in\n": "Two spaces in",
	}
	for body, want := range cases {
		if got := firstHeading([]byte(body)); got != want {
			t.Errorf("firstHeading(%q) = %q, want %q", body, got, want)
		}
	}
}
