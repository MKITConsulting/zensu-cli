package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const shellHostileText = "## Plan\n\nAdds a `--csv` switch; it's opt-in.\n$(id), \"$HOME\" and 'quotes' stay literal.\n"

func writeValueFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "value.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadValueFile(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		format        valueFormat
		want, err     string
	}{
		{"text keeps shell syntax and trims the final newline", shellHostileText, textValue, strings.TrimSuffix(shellHostileText, "\n"), ""},
		{"text trims trailing CR and LF only", "line one\r\n\r\nline two  \r\n\r\n", textValue, "line one\r\n\r\nline two  ", ""},
		{"text keeps leading indentation", "    indented code\n", textValue, "    indented code", ""},
		{"text drops a byte order mark", "\uFEFFvalue\n", textValue, "value", ""},
		{"JSON stays byte for byte", "[{\"title\":\"it's `x`\"}]\n", jsonValue, "[{\"title\":\"it's `x`\"}]\n", ""},
		{"JSON drops a byte order mark", "\uFEFF[]", jsonValue, "[]", ""},
		{"empty", "", textValue, "", "is empty"},
		{"only line breaks", "\n\r\n", textValue, "", "is empty"},
		{"only whitespace", " \t\n", textValue, "", "is empty"},
		{"only a byte order mark", "\uFEFF", textValue, "", "is empty"},
		{"empty JSON", " \n", jsonValue, "", "is empty"},
		{"invalid UTF-8", "caf\xe9\n", textValue, "", "is not valid UTF-8"},
		{"NUL byte", "a\x00b", textValue, "", "contains a NUL byte"},
		{"text at the cap", strings.Repeat("a", 256<<10), textValue, strings.Repeat("a", 256<<10), ""},
		{"text over the cap", strings.Repeat("a", 256<<10+1), textValue, "", "exceeds 256 KiB"},
		{"JSON at the cap", "[" + strings.Repeat(" ", 4<<20-2) + "]", jsonValue, "[" + strings.Repeat(" ", 4<<20-2) + "]", ""},
		{"JSON over the cap", "[" + strings.Repeat(" ", 4<<20-1) + "]", jsonValue, "", "exceeds 4 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fromStdin := range []bool{false, true} {
				path, source := "-", "stdin"
				if !fromStdin {
					path = writeValueFile(t, tc.content)
					source = path
				}
				got, err := readValueFile(strings.NewReader(tc.content), path, tc.format)
				if tc.err != "" {
					if err == nil || err.Error() != source+" "+tc.err {
						t.Fatalf("%s: err = %v, want %q", source, err, source+" "+tc.err)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", source, err)
				}
				if got != tc.want {
					t.Fatalf("%s: got %q, want %q", source, truncate(got), truncate(tc.want))
				}
			}
		})
	}
}

func truncate(s string) string {
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

func TestReadValueFile_RefusesWhatIsNotAReadableRegularFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := readValueFile(strings.NewReader("unused"), dir, textValue); err == nil || err.Error() != dir+" is not a regular file" {
		t.Errorf("directory: err = %v", err)
	}
	missing := filepath.Join(dir, "missing.md")
	_, err := readValueFile(strings.NewReader("unused"), missing, textValue)
	if err == nil || !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), "cannot read "+missing+": ") {
		t.Errorf("missing file: err = %v", err)
	}
}

type reviewRun struct {
	ran                        bool
	findings, conditions       string
	findingsSet, conditionsSet bool
}

func newValueFileTestCmd(run *reviewRun) *cobra.Command {
	var findings, conditions string
	cmd := &cobra.Command{Use: "review", SilenceUsage: true, SilenceErrors: true, RunE: func(cmd *cobra.Command, _ []string) error {
		*run = reviewRun{true, findings, conditions, cmd.Flags().Changed("findings"), cmd.Flags().Changed("conditions")}
		return nil
	}}
	cmd.Flags().StringVar(&findings, "findings", "", "review findings (Markdown)")
	cmd.Flags().StringVar(&conditions, "conditions", "", "conditions (Markdown)")
	return withValueFiles(cmd, textFile("findings"), textFile("conditions"))
}

func TestWithValueFiles_StoresTheValueInTheInlineFlag(t *testing.T) {
	path := writeValueFile(t, shellHostileText)
	var run reviewRun
	cmd := newValueFileTestCmd(&run)
	cmd.SetIn(strings.NewReader("conditions from stdin\n"))
	if err := runCmd(t, cmd, "--findings-file", path, "--conditions-file", "-"); err != nil {
		t.Fatal(err)
	}
	want := reviewRun{true, strings.TrimSuffix(shellHostileText, "\n"), "conditions from stdin", true, true}
	if run != want {
		t.Fatalf("run = %+v, want %+v", run, want)
	}
}

func TestWithValueFiles_LeavesInlineFlagsAsTheyWere(t *testing.T) {
	var run reviewRun
	if err := runCmd(t, newValueFileTestCmd(&run), "--findings", "-"); err != nil {
		t.Fatal(err)
	}
	if want := (reviewRun{true, "-", "", true, false}); run != want {
		t.Fatalf("run = %+v, want %+v", run, want)
	}
}

func TestWithValueFiles_RefusesBeforeTheCommandRuns(t *testing.T) {
	valid := writeValueFile(t, "fine\n")
	missing := filepath.Join(t.TempDir(), "missing.md")
	for _, tc := range []struct {
		name string
		args []string
		err  string
	}{
		{"inline flag and file", []string{"--findings", "x", "--findings-file", valid}, "if any flags in the group [findings findings-file] are set none of the others can be; [findings findings-file] were all set"},
		{"two flags on stdin", []string{"--findings-file", "-", "--conditions-file", "-"}, "--findings-file and --conditions-file both read stdin; pass one of them a file path"},
		{"empty path", []string{"--findings-file", ""}, "--findings-file needs a path, or - for stdin"},
		{"one bad file of two", []string{"--findings-file", valid, "--conditions-file", missing}, "--conditions-file: cannot read " + missing + ": "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var run reviewRun
			cmd := newValueFileTestCmd(&run)
			cmd.SetIn(strings.NewReader("stdin\n"))
			err := runCmd(t, cmd, tc.args...)
			if err == nil || !strings.HasPrefix(err.Error(), tc.err) {
				t.Fatalf("err = %v, want prefix %q", err, tc.err)
			}
			if run.ran {
				t.Fatal("the command ran despite the error")
			}
		})
	}
}

func TestWithValueFiles_HelpNamesPathStdinAndCap(t *testing.T) {
	var run reviewRun
	cmd := newValueFileTestCmd(&run)
	scan := &cobra.Command{Use: "scan", RunE: func(*cobra.Command, []string) error { return nil }}
	scan.Flags().String("candidates", "", "JSON array (required)")
	withValueFiles(scan, jsonFile("candidates"))
	for _, tc := range []struct {
		cmd         *cobra.Command
		flag, usage string
	}{
		{cmd, "findings-file", "read the --findings value from path, or - for stdin (max 256 KiB)"},
		{cmd, "conditions-file", "read the --conditions value from path, or - for stdin (max 256 KiB)"},
		{scan, "candidates-file", "read the --candidates JSON from path, or - for stdin (max 4 MiB)"},
	} {
		if help := tc.cmd.Flags().FlagUsages(); !strings.Contains(help, "--"+tc.flag+" path ") || !strings.Contains(help, tc.usage) {
			t.Errorf("help lacks --%s path with %q:\n%s", tc.flag, tc.usage, help)
		}
		if _, ok := tc.cmd.Flags().Lookup(tc.flag).Annotations[cobra.BashCompFilenameExt]; !ok {
			t.Errorf("--%s is not marked for file name completion", tc.flag)
		}
	}
}

var valueFileFlags = []struct {
	path []string
	flag string
	json bool
}{
	{[]string{"features", "update"}, "description", false},
	{[]string{"products", "create"}, "description", false},
	{[]string{"products", "vision-create"}, "content", false},
	{[]string{"products", "bootstrap-apply"}, "result", true},
	{[]string{"subfeatures", "add"}, "description", false},
	{[]string{"journeys", "create"}, "description", false},
	{[]string{"roadmap", "create"}, "description", false},
	{[]string{"roadmap", "update"}, "description", false},
	{[]string{"security", "review"}, "findings", false},
	{[]string{"security", "review"}, "conditions", false},
	{[]string{"wiki", "create"}, "content", false},
	{[]string{"wiki", "update"}, "content", false},
	{[]string{"link", "docs"}, "content", false},
	{[]string{"ghost", "scan"}, "candidates", true},
	{[]string{"ghost", "scan"}, "components", true},
}

func TestValueFileFlags_ExistWithTheirHelp(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range valueFileFlags {
		cmd, _, err := root.Find(tc.path)
		if err != nil || cmd.Name() != tc.path[len(tc.path)-1] {
			t.Errorf("%v: command not found (%v)", tc.path, err)
			continue
		}
		want := "read the --" + tc.flag + " value from `path`, or - for stdin (max 256 KiB)"
		if tc.json {
			want = "read the --" + tc.flag + " JSON from `path`, or - for stdin (max 4 MiB)"
		}
		flag := cmd.Flags().Lookup(tc.flag + "-file")
		if flag == nil || flag.Usage != want {
			t.Errorf("%v --%s-file: got %v, want usage %q", tc.path, tc.flag, flag, want)
		}
	}
}

func TestValueFileFlags_LeaveTheirInlineFlagUnrequiredInTheContract(t *testing.T) {
	root := NewRootCmd()
	for _, tc := range valueFileFlags {
		cmd, _, err := root.Find(tc.path)
		if err != nil {
			t.Errorf("%v: command not found (%v)", tc.path, err)
			continue
		}
		inline := cmd.Flags().Lookup(tc.flag)
		if inline == nil {
			t.Errorf("%v --%s: flag not found", tc.path, tc.flag)
			continue
		}
		if IsRequiredFlag(inline) {
			t.Errorf("%v --%s is required in docs/commands.json although --%s-file can supply it; end its usage with (required unless --%s-file): %q", tc.path, tc.flag, tc.flag, tc.flag, inline.Usage)
		}
	}
}

func TestValueFileFlags_CompleteFileNames(t *testing.T) {
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{cobra.ShellCompRequestCmd, "features", "update", "f1", "--description-file", ""})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != ":0\n" {
		t.Fatalf("completion = %q, want no suggestions and the default directive, which completes file names", got)
	}
}

func TestValueFiles_ReachTheRequestBody(t *testing.T) {
	candidates := "[{\"slug\":\"csv-export\",\"title\":\"CSV export\",\"description\":\"Adds a `--csv` switch; it's opt-in.\"}]\n"
	components := "[{\"slug\":\"exports\",\"name\":\"Exports\",\"description\":\"`$(id)` stays literal\"}]\n"
	result := "{\"components\":[],\"features\":[{\"title\":\"it's `x`\",\"slug\":\"x\"}]}\n"
	for _, tc := range []struct {
		name          string
		newCmd        func(*Factory) *cobra.Command
		args          []string
		flag, content string
		write, read   string
		field         string
	}{
		{"features update", NewFeaturesCmd, []string{"update", "f1"}, "description", shellHostileText, "PATCH /api/features/f1", "GET /api/features/f1", "description"},
		{"products create", NewProductsCmd, []string{"create", "--name", "Exports"}, "description", shellHostileText, "POST /api/products", "", "description"},
		{"products vision-create", NewProductsCmd, []string{"vision-create", "--title", "Vision"}, "content", shellHostileText, "POST /api/visions", "", "content"},
		{"products bootstrap-apply", NewProductsCmd, []string{"bootstrap-apply", "v1"}, "result", result, "POST /api/visions/v1/bootstrap/apply", "", ""},
		{"subfeatures add", NewSubfeaturesCmd, []string{"add", "f1", "--title", "Sub"}, "description", shellHostileText, "POST /api/features/f1/subfeatures", "", "description"},
		{"journeys create", NewJourneysCmd, []string{"create", "--product", "p1", "--title", "Journey"}, "description", shellHostileText, "POST /api/products/p1/journeys", "", "description"},
		{"roadmap create", NewRoadmapCmd, []string{"create", "--product", "p1", "--title", "MVP"}, "description", shellHostileText, "POST /api/products/p1/roadmaps", "", "description"},
		{"roadmap update", NewRoadmapCmd, []string{"update", "r1", "--title", "MVP"}, "description", shellHostileText, "PUT /api/roadmaps/r1", "", "description"},
		{"security review findings", NewSecurityCmd, []string{"review", "f1", "--reviewer", "me", "--status", "approved"}, "findings", shellHostileText, "POST /api/features/f1/security/review", "", "findings"},
		{"security review conditions", NewSecurityCmd, []string{"review", "f1", "--reviewer", "me", "--status", "conditional"}, "conditions", shellHostileText, "POST /api/features/f1/security/review", "", "conditions"},
		{"wiki create", NewWikiCmd, []string{"create", "--product", "p1", "--title", "Guide"}, "content", shellHostileText, "POST /api/wiki/pages", "", "content"},
		{"wiki update", NewWikiCmd, []string{"update", "w1"}, "content", shellHostileText, "PUT /api/wiki/pages/w1", "", "content"},
		{"link docs", NewLinkCmd, []string{"docs", "f1", "--doc-type", "user_facing"}, "content", shellHostileText, "POST /api/features/f1/docs", "", "content"},
		{"ghost scan candidates", NewGhostCmd, []string{"scan", "--product", "p1"}, "candidates", candidates, "POST /api/products/p1/ghost/scans", "", "candidates"},
		{"ghost scan components", NewGhostCmd, []string{"scan", "--product", "p1", "--candidates", "[]"}, "components", components, "POST /api/products/p1/ghost/scans", "", "components"},
	} {
		for _, fromStdin := range []bool{false, true} {
			name := tc.name + " from a file"
			if fromStdin {
				name = tc.name + " from stdin"
			}
			t.Run(name, func(t *testing.T) {
				routes := map[string]func(recordedRequest) fakeReply{tc.write: reply(200, map[string]any{"id": "x"})}
				if tc.read != "" {
					routes[tc.read] = reply(200, map[string]any{"id": "f1", "slug": "zen-001", "title": "Old"})
				}
				srv, api := newFakeAPI(t, routes)
				f, _ := testFactory(srv)
				cmd := tc.newCmd(f)
				path := "-"
				if fromStdin {
					cmd.SetIn(strings.NewReader(tc.content))
				} else {
					path = writeValueFile(t, tc.content)
					cmd.SetIn(strings.NewReader("stdin is not the source"))
				}
				if err := runCmd(t, cmd, append(slices.Clone(tc.args), "--"+tc.flag+"-file", path)...); err != nil {
					t.Fatal(err)
				}
				method, route, _ := strings.Cut(tc.write, " ")
				got := api.only(t, method, route)
				if tc.field == "" {
					if got.Raw != tc.content {
						t.Fatalf("body = %q, want the file byte for byte: %q", got.Raw, tc.content)
					}
					return
				}
				var want any = strings.TrimSuffix(tc.content, "\n")
				if strings.HasPrefix(tc.content, "[") {
					if err := json.Unmarshal([]byte(tc.content), &want); err != nil {
						t.Fatal(err)
					}
				}
				if a, b := jsonOf(t, got.Body[tc.field]), jsonOf(t, want); a != b {
					t.Fatalf("%s = %s, want %s", tc.field, a, b)
				}
			})
		}
	}
}

func TestValueFiles_FailBeforeAnyRequest(t *testing.T) {
	empty := writeValueFile(t, "\n")
	valid := writeValueFile(t, "Fine.\n")
	missing := filepath.Join(t.TempDir(), "missing.md")
	for _, tc := range []struct {
		name   string
		newCmd func(*Factory) *cobra.Command
		args   []string
		err    string
	}{
		{"an empty file never clears the description", NewFeaturesCmd, []string{"update", "f1", "--description-file", empty}, "--description-file: " + empty + " is empty"},
		{"a missing file", NewFeaturesCmd, []string{"update", "f1", "--description-file", missing}, "--description-file: cannot read " + missing + ": "},
		{"inline value and file", NewFeaturesCmd, []string{"update", "f1", "--description", "x", "--description-file", valid}, "if any flags in the group [description description-file] are set none of the others can be"},
		{"two values on stdin", NewSecurityCmd, []string{"review", "f1", "--reviewer", "me", "--status", "conditional", "--findings-file", "-", "--conditions-file", "-"}, "--findings-file and --conditions-file both read stdin"},
		{"JSON keeps its own validation", NewGhostCmd, []string{"scan", "--product", "p1", "--candidates-file", valid}, "--candidates must be a JSON array: "},
		{"no candidates in either form", NewGhostCmd, []string{"scan", "--product", "p1"}, "--candidates or --candidates-file is required"},
		{"no vision content in either form", NewProductsCmd, []string{"vision-create", "--title", "t"}, "--content or --content-file is required"},
		{"no bootstrap result in either form", NewProductsCmd, []string{"bootstrap-apply", "v1"}, "--result or --result-file is required"},
		{"no wiki page content in either form", NewWikiCmd, []string{"create", "--product", "p1", "--title", "t"}, "--product, --title and --content (or --content-file) are required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{})
			f, _ := testFactory(srv)
			cmd := tc.newCmd(f)
			cmd.SetIn(strings.NewReader("Fine.\n"))
			err := runCmd(t, cmd, tc.args...)
			if err == nil || !strings.HasPrefix(err.Error(), tc.err) {
				t.Fatalf("err = %v, want prefix %q", err, tc.err)
			}
			if calls := api.calls(); len(calls) != 0 {
				t.Fatalf("sent %d requests: %v", len(calls), api.summary())
			}
		})
	}
}
