package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func readSkillDoc(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "skills", "zensu", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func fencedLines(doc string) []string {
	var lines []string
	inFence := false
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			lines = append(lines, line)
		}
	}
	return lines
}

var subTokenRe = regexp.MustCompile(`^[a-z][a-z-]*$`)

type invocation struct {
	group string
	sub   string
	line  string
}

func documentedInvocations(doc string) []invocation {
	var out []invocation
	for _, raw := range fencedLines(doc) {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "zensu ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !subTokenRe.MatchString(fields[1]) {
			continue
		}
		inv := invocation{group: fields[1], line: line}
		if len(fields) > 2 && subTokenRe.MatchString(fields[2]) {
			inv.sub = fields[2]
		}
		out = append(out, inv)
	}
	return out
}

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func rootGroupNames(root *cobra.Command) map[string]bool {
	names := map[string]bool{}
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			continue
		}
		names[c.Name()] = true
	}
	return names
}

var groupTableRowRe = regexp.MustCompile("(?m)^\\| `([a-z-]+)` \\|")

func TestSkillDocs_GroupTableMatchesRootCommands(t *testing.T) {
	root := NewRootCmd()
	want := rootGroupNames(root)

	doc := readSkillDoc(t, "reference.md")
	documented := map[string]bool{}
	for _, m := range groupTableRowRe.FindAllStringSubmatch(doc, -1) {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("reference.md command-group table not found — parser or doc structure broke")
	}

	for name := range want {
		if !documented[name] {
			t.Errorf("command group %q exists in the CLI but is missing from the reference.md group table", name)
		}
	}
	for name := range documented {
		if !want[name] {
			t.Errorf("reference.md group table documents %q, which is not a CLI command group", name)
		}
	}
}

func TestSkillDocs_DocumentedInvocationsExist(t *testing.T) {
	root := NewRootCmd()
	for _, file := range []string{"SKILL.md", "reference.md"} {
		doc := readSkillDoc(t, file)
		invs := documentedInvocations(doc)
		if len(invs) == 0 {
			t.Fatalf("%s: no zensu invocations found in fenced code blocks — parser or doc structure broke", file)
		}
		for _, inv := range invs {
			group := findChild(root, inv.group)
			if group == nil {
				t.Errorf("%s documents unknown command group %q (line: %s)", file, inv.group, inv.line)
				continue
			}
			if inv.sub != "" && findChild(group, inv.sub) == nil {
				t.Errorf("%s documents unknown command %q %q (line: %s)", file, inv.group, inv.sub, inv.line)
			}
		}
	}
}

func TestSkillDocs_EverySubcommandIsDocumented(t *testing.T) {
	root := NewRootCmd()
	documented := map[string]bool{}
	for _, file := range []string{"SKILL.md", "reference.md"} {
		for _, inv := range documentedInvocations(readSkillDoc(t, file)) {
			if inv.sub != "" {
				documented[inv.group+" "+inv.sub] = true
			}
		}
	}
	for _, group := range root.Commands() {
		if group.Name() == "help" || group.Name() == "completion" || group.Hidden || !group.HasSubCommands() {
			continue
		}
		for _, sub := range group.Commands() {
			if sub.Name() == "help" || sub.Hidden {
				continue
			}
			if !documented[group.Name()+" "+sub.Name()] {
				t.Errorf("command %q %q exists in the CLI but no fenced invocation documents it", group.Name(), sub.Name())
			}
		}
	}
}

func TestReadme_GroupTableMatchesRootCommands(t *testing.T) {
	root := NewRootCmd()
	want := rootGroupNames(root)

	b, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	documented := map[string]bool{}
	for _, m := range groupTableRowRe.FindAllStringSubmatch(string(b), -1) {
		documented[m[1]] = true
	}
	if len(documented) == 0 {
		t.Fatal("README.md command-group table not found — parser or doc structure broke")
	}

	for name := range want {
		if !documented[name] {
			t.Errorf("command group %q exists in the CLI but is missing from the README command table", name)
		}
	}
	for name := range documented {
		if !want[name] {
			t.Errorf("README command table documents %q, which is not a CLI command group", name)
		}
	}

}

func TestSkillDocs_SkillCommandMapCoversAllGroups(t *testing.T) {
	root := NewRootCmd()
	doc := readSkillDoc(t, "SKILL.md")
	idx := strings.Index(doc, "## Full command map")
	if idx < 0 {
		t.Fatal("SKILL.md is missing the '## Full command map' section")
	}
	section := doc[idx:]
	for name := range rootGroupNames(root) {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		if !re.MatchString(section) {
			t.Errorf("SKILL.md command map does not mention command group %q", name)
		}
	}
}
