package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

type heredoc struct {
	delimiter string
	stripTabs bool
}

func (h heredoc) closedBy(line string) bool {
	if h.stripTabs {
		line = strings.TrimLeft(line, "\t")
	}
	return line == h.delimiter
}

var shellFenceLanguages = []string{"", "bash", "sh", "shell", "zsh", "console"}

func fencedLines(doc string) []string {
	lines, _ := scanFencedLines(doc)
	return lines
}

func scanFencedLines(doc string) (lines, unclosed []string) {
	inFence, shell := false, false
	var pending, open []heredoc
	endBlock := func() {
		for _, h := range append(open, pending...) {
			unclosed = append(unclosed, h.delimiter)
		}
		pending, open = nil, nil
	}
	for _, line := range strings.Split(doc, "\n") {
		if fence, ok := strings.CutPrefix(strings.TrimSpace(line), "```"); ok {
			endBlock()
			inFence = !inFence
			shell = slices.Contains(shellFenceLanguages, strings.TrimSpace(fence))
			continue
		}
		if !inFence {
			continue
		}
		if len(open) > 0 {
			if open[0].closedBy(line) {
				open = open[1:]
			}
			continue
		}
		lines = append(lines, line)
		if !shell {
			continue
		}
		_, opened := lexLine(line)
		pending = append(pending, opened...)
		if !strings.HasSuffix(strings.TrimSpace(line), "\\") {
			open, pending = pending, nil
		}
	}
	endBlock()
	return lines, unclosed
}

var subTokenRe = regexp.MustCompile(`^[a-z][a-z-]*$`)

type invocation struct {
	group  string
	sub    string
	line   string
	tokens []string
}

func documentedInvocations(doc string) []invocation {
	var out []invocation
	lines := fencedLines(doc)
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "zensu ") {
			continue
		}
		tokens, heredocs := lexLine(line)
		opened := len(heredocs) > 0
		for i+1 < len(lines) && continuesInvocation(lines[i], lines[i+1], opened) {
			i++
			line = strings.TrimSpace(strings.TrimSuffix(line, "\\")) + " " + strings.TrimSpace(lines[i])
			more, alsoOpened := lexLine(lines[i])
			tokens, opened = append(tokens, more...), opened || len(alsoOpened) > 0
		}
		if len(tokens) < 2 || !subTokenRe.MatchString(tokens[1]) {
			continue
		}
		inv := invocation{group: tokens[1], line: line, tokens: tokens[1:]}
		if len(tokens) > 2 && subTokenRe.MatchString(tokens[2]) {
			inv.sub = tokens[2]
		}
		out = append(out, inv)
	}
	return out
}

func continuesInvocation(line, next string, heredocOpened bool) bool {
	if strings.HasSuffix(strings.TrimSpace(line), "\\") {
		return true
	}
	if heredocOpened {
		return false
	}
	trimmed := strings.TrimSpace(next)
	indented := strings.TrimLeft(next, " \t") != next
	return indented && trimmed != "" && !strings.HasPrefix(trimmed, "zensu ")
}

func lexLine(line string) ([]string, []heredoc) {
	var tokens []string
	var heredocs []heredoc
	var cur strings.Builder
	var quote rune
	substitution, nested := 0, 0
	placeholder := false
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
		nested = 0
	}
	runes := []rune(strings.TrimSuffix(strings.TrimSpace(line), "\\"))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case substitution > 0:
			cur.WriteRune(r)
			if r == '(' {
				substitution++
			} else if r == ')' {
				substitution--
			}
		case placeholder:
			cur.WriteRune(r)
			placeholder = r != '>'
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == '$' && i+1 < len(runes) && runes[i+1] == '(':
			substitution = 1
			cur.WriteString("$(")
			i++
		case r == '#' && cur.Len() == 0:
			flush()
			return tokens, heredocs
		case r == '<' && i+1 < len(runes) && runes[i+1] == '<' && cur.Len() == 0:
			start := i + 2
			hereString := start < len(runes) && runes[start] == '<'
			stripTabs := !hereString && start < len(runes) && runes[start] == '-'
			if hereString || stripTabs {
				start++
			}
			word, end := shellWord(runes, start)
			if !hereString && word != "" {
				heredocs = append(heredocs, heredoc{delimiter: word, stripTabs: stripTabs})
			}
			i = end - 1
		case r == '<' && cur.Len() == 0:
			placeholder = true
			cur.WriteRune(r)
		case unicode.IsSpace(r):
			flush()
		case (r == '[' || r == '(') && cur.Len() == 0:
			tokens = append(tokens, string(r))
		case r == '[' || r == '(':
			nested++
			cur.WriteRune(r)
		case (r == ']' || r == ')') && nested > 0:
			nested--
			cur.WriteRune(r)
		case r == ']' || r == ')':
			flush()
			tokens = append(tokens, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens, heredocs
}

func shellWord(runes []rune, start int) (string, int) {
	i := start
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	var word strings.Builder
	var quote rune
	for ; i < len(runes); i++ {
		r := runes[i]
		switch {
		case quote != 0 && r == quote:
			quote = 0
		case quote != 0:
			word.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == '\\' && i+1 < len(runes):
			i++
			word.WriteRune(runes[i])
		case unicode.IsSpace(r) || strings.ContainsRune("&;|<>()", r):
			return word.String(), i
		default:
			word.WriteRune(r)
		}
	}
	return word.String(), i
}

type documentedArg struct {
	text     string
	optional bool
}

func resolveCommand(root *cobra.Command, tokens []string) (*cobra.Command, []string) {
	cmd := root
	for len(tokens) > 0 {
		child := findChild(cmd, tokens[0])
		if child == nil {
			break
		}
		cmd, tokens = child, tokens[1:]
	}
	return cmd, tokens
}

func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	if flag := cmd.LocalFlags().Lookup(name); flag != nil {
		return flag
	}
	return cmd.InheritedFlags().Lookup(name)
}

func isFlagValue(token string) bool {
	switch token {
	case "[", "]", "(", ")", "|":
		return false
	}
	return !strings.HasPrefix(token, "--")
}

func invocationProblems(root *cobra.Command, tokens []string) []string {
	cmd, rest := resolveCommand(root, tokens)
	cmd.InitDefaultHelpFlag()
	var problems []string
	shown := map[string]bool{}
	var args []documentedArg
	var groups []string
walk:
	for i := 0; i < len(rest); i++ {
		token := rest[i]
		optional := slices.Contains(groups, "[")
		switch {
		case token == "[" || token == "(":
			groups = append(groups, token)
		case token == "]" || token == ")":
			if len(groups) > 0 {
				groups = groups[:len(groups)-1]
			}
		case token == "&&" || token == "||" || token == ";":
			break walk
		case token == "|" && len(groups) == 0:
			break walk
		case token == "|" || token == "...":
		case strings.HasPrefix(token, "--"):
			name, _, inline := strings.Cut(strings.TrimPrefix(token, "--"), "=")
			flag := lookupFlag(cmd, name)
			if flag == nil {
				problems = append(problems, fmt.Sprintf("--%s is not a flag of %q", name, cmd.CommandPath()))
				continue
			}
			if !optional {
				shown[name] = true
			}
			if !inline && flag.NoOptDefVal == "" && i+1 < len(rest) && isFlagValue(rest[i+1]) {
				i++
			}
		default:
			args = append(args, documentedArg{text: token, optional: optional})
		}
	}
	if !cmd.Runnable() {
		return problems
	}
	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if IsRequiredFlag(flag) && !shown[flag.Name] {
			problems = append(problems, fmt.Sprintf("required flag --%s of %q is missing or shown as optional", flag.Name, cmd.CommandPath()))
		}
	})
	var minimal, full []string
	for _, arg := range args {
		value := arg.text
		if strings.HasPrefix(value, "<") {
			value = PlaceholderArg
		}
		full = append(full, value)
		if !arg.optional {
			minimal = append(minimal, value)
		}
	}
	checks := [][]string{minimal}
	if len(full) > len(minimal) {
		checks = append(checks, full)
	}
	for _, values := range checks {
		if err := cmd.ValidateArgs(values); err != nil {
			problems = append(problems, fmt.Sprintf("%q rejects the positional arguments: %v", cmd.CommandPath(), err))
		}
	}
	return problems
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

func TestSkillDocs_DocumentedInvocationsUseRealFlagsAndArgs(t *testing.T) {
	for _, file := range []string{"SKILL.md", "reference.md"} {
		root := NewRootCmd()
		for _, inv := range documentedInvocations(readSkillDoc(t, file)) {
			for _, problem := range invocationProblems(root, inv.tokens) {
				t.Errorf("%s: %s (line: %s)", file, problem, inv.line)
			}
		}
	}
}

func TestInvocationProblems_ReadTheDocumentedNotation(t *testing.T) {
	newRoot := func() *cobra.Command {
		get := &cobra.Command{Use: "get <id>", Args: cobra.ExactArgs(1), RunE: func(*cobra.Command, []string) error { return nil }}
		get.Flags().String("product", "", "product ID (required)")
		get.Flags().String("url", "", "link")
		get.Flags().String("file", "", "file as path[:type]")
		get.Flags().Bool("json", false, "output raw JSON")
		promote := &cobra.Command{Use: "promote [parent] <id>", Args: cobra.RangeArgs(1, 2), RunE: func(*cobra.Command, []string) error { return nil }}
		things := &cobra.Command{Use: "things"}
		things.AddCommand(get, promote)
		root := &cobra.Command{Use: "zensu"}
		root.PersistentFlags().String("api-url", "", "API base URL")
		root.AddCommand(things)
		return root
	}
	for _, tc := range []struct {
		doc  string
		want []string
	}{
		{"zensu things get <id> --product <p> [--json] --api-url https://zensu.test", nil},
		{`zensu things get <id> --product "$(git rev-parse --short HEAD)"   # --short is git's, --bogus a comment`, nil},
		{"zensu things get <id> --product p --url <link to the page> --file path[:type[:language]]", nil},
		{"zensu things get <id> --product p --json | jq -r '.data[].id' --raw-output", nil},
		{"zensu things get <id> --product p (--url u | --file f) --help", nil},
		{"zensu things get <id> --product p --file - <<'ZENSU_VALUE_1A2B3C'", nil},
		{"zensu things get <id> --product p --file - <<'ZENSU_VALUE_1A2B3C' --raw", []string{`--raw is not a flag of "zensu things get"`}},
		{"zensu things get <id> --product p --file - <<-ZENSU_VALUE_1A2B3C && echo sent", nil},
		{`zensu things get <id> --product p --file - <<< "$value" --json`, nil},
		{"zensu things get <id> \\\n  --product p", nil},
		{"zensu things get <id>\n    --product p   # reference.md style continuation", nil},
		{"zensu things promote [parent] <id>", nil},
		{"zensu things get <id> --product p --raw", []string{`--raw is not a flag of "zensu things get"`}},
		{"zensu things get <id> --json", []string{`required flag --product of "zensu things get" is missing or shown as optional`}},
		{"zensu things get <id> [--product p]", []string{`required flag --product of "zensu things get" is missing or shown as optional`}},
		{"zensu things get --product p", []string{`"zensu things get" rejects the positional arguments: accepts 1 arg(s), received 0`}},
		{"zensu things get <id> ...", []string{`required flag --product of "zensu things get" is missing or shown as optional`}},
		{"zensu things promote [parent] <id> <extra>", []string{`"zensu things promote" rejects the positional arguments: accepts between 1 and 2 arg(s), received 3`}},
	} {
		invs := documentedInvocations("```bash\n" + tc.doc + "\n```\n")
		if len(invs) != 1 {
			t.Errorf("%q parsed into %d invocations, want 1", tc.doc, len(invs))
			continue
		}
		if got := invocationProblems(newRoot(), invs[0].tokens); !slices.Equal(got, tc.want) {
			t.Errorf("%q:\n got %q\nwant %q", tc.doc, got, tc.want)
		}
	}
}

func TestDocumentedInvocations_SkipHeredocBodies(t *testing.T) {
	doc := "```bash\n" +
		"zensu things get <id> --product p --file - <<'ZENSU_VALUE_1A2B3C'\n" +
		"  zensu things purge --everything\n" +
		"  --raw\n" +
		"ZENSU_VALUE_1A2B3C\n" +
		"cat >| \"$f\" <<-EOF &&\n" +
		"\tzensu things purge --everything\n" +
		"\tEOF\n" +
		"zensu things get <id> --product p \\\n" +
		"  --file - <<'ZENSU_VALUE_4D5E6F'\n" +
		"    --raw\n" +
		"ZENSU_VALUE_4D5E6F\n" +
		"  --not-a-continuation\n" +
		"zensu things promote <id>\n" +
		"```\n" +
		"```go\n" +
		"limit := 1 << 20\n" +
		"```\n"
	var got []string
	for _, inv := range documentedInvocations(doc) {
		got = append(got, strings.Join(inv.tokens, " "))
	}
	want := []string{
		"things get <id> --product p --file -",
		"things get <id> --product p --file -",
		"things promote <id>",
	}
	if !slices.Equal(got, want) {
		t.Errorf("invocations:\n got %q\nwant %q", got, want)
	}
	if _, unclosed := scanFencedLines(doc); len(unclosed) > 0 {
		t.Errorf("unclosed = %q, want none", unclosed)
	}
}

func TestScanFencedLines_ReportsAHeredocItsBlockNeverCloses(t *testing.T) {
	doc := "```bash\n" +
		"zensu things get <id> --file - <<'ZENSU_VALUE_1A2B3C'\n" +
		"body\n" +
		"ZENSU_VALUE_1A2B3\n" +
		"```\n\n" +
		"```bash\n" +
		"zensu things promote <id>\n" +
		"```\n"
	lines, unclosed := scanFencedLines(doc)
	if want := []string{"ZENSU_VALUE_1A2B3C"}; !slices.Equal(unclosed, want) {
		t.Errorf("unclosed = %q, want %q", unclosed, want)
	}
	if want := []string{"zensu things get <id> --file - <<'ZENSU_VALUE_1A2B3C'", "zensu things promote <id>"}; !slices.Equal(lines, want) {
		t.Errorf("lines = %q, want %q", lines, want)
	}
}

func TestSkillDocs_EveryHeredocClosesInItsCodeBlock(t *testing.T) {
	for _, file := range []string{"SKILL.md", "reference.md"} {
		if _, unclosed := scanFencedLines(readSkillDoc(t, file)); len(unclosed) > 0 {
			t.Errorf("%s: heredocs never closed in their code block: %q", file, unclosed)
		}
	}
}

var groupsWithoutJSON = []string{"auth", "completion", "meta"}

var commandsWithoutResponseData = []string{
	"journeys step-delete",
	"roadmap add-feature",
	"roadmap delete",
	"roadmap milestone-delete",
	"roadmap remove-feature",
	"work repositories remove",
}

func TestSkillDocs_EveryCommandThatReturnsDataAcceptsJSON(t *testing.T) {
	root := NewRootCmd()
	seen := map[string]bool{}
	VisitCommands(root, func(cmd *cobra.Command) {
		if cmd.Name() == "help" || !cmd.Runnable() {
			return
		}
		path := strings.TrimPrefix(cmd.CommandPath(), root.Name()+" ")
		group, _, _ := strings.Cut(path, " ")
		if slices.Contains(groupsWithoutJSON, group) {
			return
		}
		hasJSON := cmd.Flags().Lookup("json") != nil
		switch {
		case slices.Contains(commandsWithoutResponseData, path):
			seen[path] = true
			if hasJSON {
				t.Errorf("%q is listed as returning no data but defines --json; take it off commandsWithoutResponseData", path)
			}
		case !hasJSON:
			t.Errorf("%q has no --json flag; SKILL.md ground rule 2 promises it on every command that returns data", path)
		}
	})
	for _, path := range commandsWithoutResponseData {
		if !seen[path] {
			t.Errorf("commandsWithoutResponseData lists %q, which is not a CLI command", path)
		}
	}
	for _, file := range []string{"SKILL.md", "reference.md"} {
		doc := readSkillDoc(t, file)
		for _, name := range append(slices.Clone(groupsWithoutJSON), commandsWithoutResponseData...) {
			if !strings.Contains(doc, "`"+name+"`") {
				t.Errorf("%s does not name `%s` among the commands without --json", file, name)
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

	row := regexp.MustCompile("(?m)^\\| `mocks` \\| (.+) \\|$").FindStringSubmatch(string(b))
	if row == nil {
		t.Fatal("README command table has no `mocks` row")
	}
	if !strings.Contains(strings.ToLower(row[1]), "upload") {
		t.Errorf("the README `mocks` row must name the write path, got %q", row[1])
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
