package cmd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var requiredMarkerRe = regexp.MustCompile(`(?:\(|[;,] )required\)`)

var freeTextFlagNames = []string{
	"conditions",
	"description",
	"expected-result",
	"findings",
	"reason",
	"reject-reason",
	"scope-details",
	"scope-summary",
}

var freeTextFlagsWithoutFormat = map[string]string{
	"work event --reason":   "short prose for the work channel; its web rendering is unchecked",
	"work release --reason": "short prose for the work channel; its web rendering is unchecked",
}

func freeTextFormatProblems(root *cobra.Command, names []string, exempt map[string]string) []string {
	var problems []string
	seenNames := map[string]bool{}
	seenFlags := map[string]bool{}
	VisitCommands(root, func(cmd *cobra.Command) {
		if cmd.Name() == "help" || !cmd.Runnable() {
			return
		}
		path := strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
		cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
			if !slices.Contains(names, flag.Name) {
				return
			}
			key := path + " --" + flag.Name
			seenNames[flag.Name] = true
			seenFlags[key] = true
			markdown := strings.Contains(flag.Usage, " (Markdown)")
			plain := strings.Contains(flag.Usage, " (plain text)")
			_, exempted := exempt[key]
			switch {
			case exempted && (markdown || plain):
				problems = append(problems, key+" names its format; remove it from freeTextFlagsWithoutFormat")
			case !exempted && markdown == plain:
				problems = append(problems, fmt.Sprintf("%s must name exactly one format, (Markdown) or (plain text): %q", key, flag.Usage))
			}
			if requiredMarkerRe.MatchString(flag.Usage) && !requiredFlagRe.MatchString(flag.Usage) {
				problems = append(problems, key+" must keep (required) last; put the format marker before it")
			}
		})
	})
	for _, name := range names {
		if !seenNames[name] {
			problems = append(problems, "no command has a --"+name+" flag; drop it from freeTextFlagNames")
		}
	}
	for key := range exempt {
		if !seenFlags[key] {
			problems = append(problems, key+" does not exist; drop it from freeTextFlagsWithoutFormat")
		}
	}
	slices.Sort(problems)
	return problems
}

func TestFreeTextFlags_EveryListedFlagNamesItsFormat(t *testing.T) {
	for _, problem := range freeTextFormatProblems(NewRootCmd(), freeTextFlagNames, freeTextFlagsWithoutFormat) {
		t.Error(problem)
	}
}

func TestFreeTextFormatProblems_ReportUnlabelledAndStaleEntries(t *testing.T) {
	run := func(*cobra.Command, []string) error { return nil }
	create := &cobra.Command{Use: "create", RunE: run}
	create.Flags().String("description", "", "thing description")
	create.Flags().String("reason", "", "why (plain text)")
	create.Flags().String("scope-summary", "", "scope (required) (plain text)")
	update := &cobra.Command{Use: "update", RunE: run}
	update.Flags().String("description", "", "thing description (Markdown) (plain text)")
	update.Flags().String("reason", "", "why (Markdown)")
	update.Flags().String("scope-summary", "", "scope (plain text) (required)")
	things := &cobra.Command{Use: "things"}
	things.AddCommand(create, update)
	root := &cobra.Command{Use: "zensu"}
	root.AddCommand(things)
	got := freeTextFormatProblems(root, []string{"description", "reason", "scope-summary", "summary"}, map[string]string{
		"things update --reason": "names a format",
		"things delete --reason": "no such command",
	})
	want := []string{
		`things create --description must name exactly one format, (Markdown) or (plain text): "thing description"`,
		`things update --description must name exactly one format, (Markdown) or (plain text): "thing description (Markdown) (plain text)"`,
		"things create --scope-summary must keep (required) last; put the format marker before it",
		"things update --reason names its format; remove it from freeTextFlagsWithoutFormat",
		"things delete --reason does not exist; drop it from freeTextFlagsWithoutFormat",
		"no command has a --summary flag; drop it from freeTextFlagNames",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFreeTextFlags_NameTheirTextFormat(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range []struct {
		path  []string
		flag  string
		usage string
	}{
		{[]string{"features", "update"}, "description", "new description (Markdown)"},
		{[]string{"products", "create"}, "description", "product description (Markdown)"},
		{[]string{"subfeatures", "add"}, "description", "sub-feature description (Markdown)"},
		{[]string{"journeys", "create"}, "description", "journey description (Markdown)"},
		{[]string{"roadmap", "create"}, "description", "roadmap description (Markdown)"},
		{[]string{"roadmap", "update"}, "description", "roadmap description (Markdown)"},
		{[]string{"security", "review"}, "findings", "review findings (Markdown)"},
		{[]string{"security", "review"}, "conditions", "conditions for conditional approval (Markdown)"},
		{[]string{"ghost", "scan"}, "candidates", `JSON array of candidate objects; each "description" is Markdown (required unless --candidates-file)`},
		{[]string{"ghost", "scan"}, "components", `optional JSON array of component objects; each "description" is Markdown`},
		{[]string{"tiers", "create"}, "description", "tier description (plain text)"},
		{[]string{"journeys", "step"}, "description", "step description (plain text)"},
		{[]string{"journeys", "step"}, "expected-result", "expected outcome of this step (plain text)"},
		{[]string{"journeys", "step-update"}, "description", `step description (plain text); pass "" to clear`},
		{[]string{"journeys", "step-update"}, "expected-result", `expected outcome of this step (plain text); pass "" to clear`},
		{[]string{"features", "revision"}, "scope-summary", "brief summary of the revision scope (plain text) (required)"},
		{[]string{"features", "revision"}, "scope-details", "detailed scope description (plain text)"},
		{[]string{"features", "deprecate"}, "reason", "reason for deprecation (plain text)"},
		{[]string{"features", "split"}, "reason", "reason for splitting (plain text)"},
		{[]string{"features", "merge"}, "reason", "reason for merging (plain text)"},
		{[]string{"ghost", "reject"}, "reason", "rejection reason (plain text)"},
		{[]string{"ghost", "batch"}, "reject-reason", "reason for rejecting candidates (plain text) (applies to all rejected)"},
	} {
		cmd, _, err := root.Find(tc.path)
		if err != nil || cmd.Name() != tc.path[len(tc.path)-1] {
			t.Errorf("%v: command not found (%v)", tc.path, err)
			continue
		}
		flag := cmd.Flags().Lookup(tc.flag)
		if flag == nil {
			t.Errorf("%v has no --%s flag", tc.path, tc.flag)
			continue
		}
		if flag.Usage != tc.usage {
			t.Errorf("%v --%s help = %q, want %q", tc.path, tc.flag, flag.Usage, tc.usage)
		}
	}
}

func TestBootstrapApplyHelp_NamesMarkdownDescriptions(t *testing.T) {
	apply, _, err := NewRootCmd().Find([]string{"products", "bootstrap-apply"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "Component, feature and sub-feature descriptions are Markdown."; !strings.HasSuffix(apply.Long, want) {
		t.Errorf("bootstrap-apply help must end with %q, got:\n%s", want, apply.Long)
	}
}

func TestGhostScanHelp_ShowsCandidateAndComponentSchema(t *testing.T) {
	scan, _, err := NewRootCmd().Find([]string{"ghost", "scan"})
	if err != nil {
		t.Fatal(err)
	}
	_, schema, ok := strings.Cut(scan.Long, "Schema: --candidates ")
	if !ok {
		t.Fatalf("ghost scan help shows no candidate schema:\n%s", scan.Long)
	}
	candidates, components, ok := strings.Cut(schema, ", --components ")
	if !ok {
		t.Fatalf("ghost scan help shows no component schema:\n%s", scan.Long)
	}
	components, _, _ = strings.Cut(components, "]. ")
	for _, key := range []string{"slug", "title", "description", "componentSlug", "confidenceScore", "detectedTestFiles", "detectedSourceFiles", "detectedDocFiles", "priority", "estimatedEffort", "securityClassification", "sourceFileAnalyses"} {
		if !strings.Contains(candidates, `"`+key+`":`) {
			t.Errorf("candidate schema lacks %q: %s", key, candidates)
		}
	}
	for _, key := range []string{"slug", "name", "description"} {
		if !strings.Contains(components, `"`+key+`":`) {
			t.Errorf("component schema lacks %q: %s", key, components)
		}
	}
}
