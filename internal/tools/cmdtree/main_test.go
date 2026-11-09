package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/cmd"
)

func noop(*cobra.Command, []string) error { return nil }

func syntheticRoot(t *testing.T) *cobra.Command {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	root := &cobra.Command{Use: "zensu", Version: "test"}
	root.PersistentFlags().String("api-url", "", "API base URL")

	get := &cobra.Command{Use: "get <id>", Args: cobra.ExactArgs(1), RunE: noop}
	get.Flags().String("product", "", "product ID (required)")
	get.Flags().BoolP("json", "j", false, "output raw JSON")
	get.Flags().String("format", "", "output format")
	get.Flags().Lookup("format").NoOptDefVal = "table"
	get.Flags().String("legacy", "", "legacy field")
	check(get.Flags().MarkHidden("legacy"))
	get.Flags().String("old", "", "old field")
	check(get.Flags().MarkDeprecated("old", "use --format"))
	get.Flags().String("name", "", "name of the thing")
	check(get.MarkFlagRequired("name"))

	things := &cobra.Command{Use: "things", Aliases: []string{"thing", "items"}}
	things.PersistentFlags().String("scope", "", "scope of every things command")
	things.AddCommand(
		get,
		&cobra.Command{Use: "list", RunE: noop},
		&cobra.Command{Use: "promote [parent] <id>", Args: cobra.RangeArgs(1, 2), RunE: noop},
		&cobra.Command{Use: "internal", Hidden: true, Args: cobra.NoArgs, RunE: noop},
		&cobra.Command{Use: "retired", Deprecated: "use get", Args: cobra.NoArgs, RunE: noop},
	)
	root.AddCommand(things)
	return root
}

func argRange(a argCounts) string {
	if a.Max == nil {
		return fmt.Sprintf("%d..", a.Min)
	}
	return fmt.Sprintf("%d..%d", a.Min, *a.Max)
}

func TestDescribe_KeysCommandsByPathWithAliasesStatusAndArgumentCounts(t *testing.T) {
	tree, err := describe(syntheticRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if tree.SchemaVersion != 1 || tree.Generator != "go run ./internal/tools/cmdtree" || tree.Root != "zensu" {
		t.Errorf("header = %d %q %q", tree.SchemaVersion, tree.Generator, tree.Root)
	}
	wantPaths := []string{"zensu", "zensu help", "zensu things", "zensu things get", "zensu things internal", "zensu things list", "zensu things promote", "zensu things retired"}
	if got := slices.Sorted(maps.Keys(tree.Commands)); !slices.Equal(got, wantPaths) {
		t.Fatalf("command paths = %q, want %q", got, wantPaths)
	}

	things := tree.Commands["zensu things"]
	if !slices.Equal(things.Aliases, []string{"items", "thing"}) {
		t.Errorf("things aliases = %q, want them sorted", things.Aliases)
	}
	if want := []string{"get", "internal", "list", "promote", "retired"}; !slices.Equal(things.Subcommands, want) {
		t.Errorf("things subcommands = %q, want %q", things.Subcommands, want)
	}
	if want := []string{"help", "things"}; !slices.Equal(tree.Commands["zensu"].Subcommands, want) {
		t.Errorf("root subcommands = %q, want %q (cobra's help command included)", tree.Commands["zensu"].Subcommands, want)
	}
	if got := tree.Commands["zensu things get"].Aliases; got == nil || len(got) != 0 {
		t.Errorf("a command without aliases must encode them as [], got %#v", got)
	}

	for path, want := range map[string]struct {
		use      string
		runnable bool
		args     string
	}{
		"zensu":                 {"zensu", false, "0..0"},
		"zensu help":            {"help [command]", true, "0.."},
		"zensu things":          {"things", false, "0..0"},
		"zensu things get":      {"get <id>", true, "1..1"},
		"zensu things list":     {"list", true, "0.."},
		"zensu things promote":  {"promote [parent] <id>", true, "1..2"},
		"zensu things internal": {"internal", true, "0..0"},
	} {
		got := tree.Commands[path]
		if got.Use != want.use || got.Runnable != want.runnable || argRange(got.Args) != want.args {
			t.Errorf("%s: use %q runnable %v args %s, want %q %v %s", path, got.Use, got.Runnable, argRange(got.Args), want.use, want.runnable, want.args)
		}
	}
	if !tree.Commands["zensu things internal"].Hidden || tree.Commands["zensu things get"].Hidden {
		t.Error("hidden must follow cobra's Hidden field")
	}
	if !tree.Commands["zensu things retired"].Deprecated || tree.Commands["zensu things get"].Deprecated {
		t.Error("deprecated must follow cobra's Deprecated field")
	}
}

func TestDescribe_ListsEveryFlagACommandAccepts(t *testing.T) {
	tree, err := describe(syntheticRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]map[string]flagSpec{
		"zensu": {
			"api-url": {TakesValue: true, Persistent: true},
			"help":    {Shorthand: "h"},
			"version": {Shorthand: "v"},
		},
		"zensu things": {
			"api-url": {TakesValue: true, Persistent: true},
			"scope":   {TakesValue: true, Persistent: true},
			"help":    {Shorthand: "h"},
		},
		"zensu things get": {
			"api-url": {TakesValue: true, Persistent: true},
			"scope":   {TakesValue: true, Persistent: true},
			"product": {TakesValue: true, Required: true},
			"name":    {TakesValue: true, Required: true},
			"json":    {Shorthand: "j"},
			"format":  {},
			"legacy":  {TakesValue: true, Hidden: true},
			"old":     {TakesValue: true, Hidden: true, Deprecated: true},
			"help":    {Shorthand: "h"},
		},
	} {
		if got := tree.Commands[path].Flags; !maps.Equal(got, want) {
			t.Errorf("%s flags:\n got %+v\nwant %+v", path, got, want)
		}
	}
}

func TestDescribe_RefusesArgumentCountsTheSchemaCannotExpress(t *testing.T) {
	root := &cobra.Command{Use: "zensu"}
	root.AddCommand(
		&cobra.Command{Use: "shell <name>", ValidArgs: []string{"bash"}, Args: cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs), RunE: noop},
		&cobra.Command{Use: "pair [left right]", Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 || len(args) == 2 {
				return nil
			}
			return errors.New("want no argument or two")
		}, RunE: noop},
	)
	_, err := describe(root)
	if err == nil {
		t.Fatal("describe accepted argument counts that commands.json cannot express")
	}
	for _, want := range []string{
		`"zensu shell" accepts no positional argument count from 0 to 64`,
		`"zensu pair" accepts the positional argument counts [0 2]`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestEncode_IsByteStableAndKeepsUsageReadable(t *testing.T) {
	encodeTree := func() []byte {
		tree, err := describe(syntheticRoot(t))
		if err != nil {
			t.Fatal(err)
		}
		data, err := encode(tree)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := encodeTree(), encodeTree()
	if !bytes.Equal(first, second) {
		t.Fatal("two descriptions of the same tree encoded different bytes")
	}
	out := string(first)
	for _, want := range []string{
		"{\n  \"schemaVersion\": 1,\n  \"generator\": \"go run ./internal/tools/cmdtree\",\n  \"root\": \"zensu\",\n  \"commands\": {\n    \"zensu\": {\n",
		`"use": "get <id>",`,
		`"max": null`,
		`"aliases": [],`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("encoded contract does not contain %q", want)
		}
	}
	if !strings.HasSuffix(out, "}\n") {
		t.Error("encoded contract must end with a newline")
	}
	var decoded contract
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("encoded contract is not valid JSON: %v", err)
	}
}

func TestRun_WritesTheGeneratedContract(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.json")
	var stdout bytes.Buffer
	if err := run([]string{"-out", path}, &stdout); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, generated) {
		t.Error("run wrote something other than the generated contract")
	}
	if stdout.String() != "wrote "+path+"\n" {
		t.Errorf("run printed %q", stdout.String())
	}
}

func TestRun_RejectsArgumentsAndUnwritableTargets(t *testing.T) {
	if err := run([]string{"extra"}, io.Discard); err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Errorf("positional argument: err = %v", err)
	}
	if err := run([]string{"-bogus"}, io.Discard); err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -bogus") {
		t.Errorf("unknown flag: err = %v", err)
	}
	if err := run([]string{"-out", filepath.Join(t.TempDir(), "missing", "commands.json")}, io.Discard); err == nil {
		t.Error("writing into a missing directory must fail")
	}
}

func TestRealTree_ExposesRuntimeDefaultsAndGlobalFlags(t *testing.T) {
	tree, err := describe(cmd.NewRootCmd())
	if err != nil {
		t.Fatal(err)
	}
	if version, ok := tree.Commands["zensu"].Flags["version"]; !ok || version.Shorthand != "v" || version.TakesValue {
		t.Errorf("zensu --version = %+v, %v; want -v without a value", version, ok)
	}
	for path, c := range tree.Commands {
		if help, ok := c.Flags["help"]; !ok || help.Shorthand != "h" || help.TakesValue {
			t.Errorf("%s --help = %+v, %v; want -h without a value", path, help, ok)
		}
		wantPersistent := path != "zensu auth login"
		if apiURL, ok := c.Flags["api-url"]; !ok || !apiURL.TakesValue || apiURL.Persistent != wantPersistent {
			t.Errorf("%s --api-url = %+v, %v; want a value-taking flag with persistent=%v (zensu auth login defines its own local --api-url)", path, apiURL, ok, wantPersistent)
		}
		if _, ok := c.Flags["version"]; ok && path != "zensu" {
			t.Errorf("%s lists --version, which only the root accepts", path)
		}
		for _, sub := range c.Subcommands {
			if _, ok := tree.Commands[path+" "+sub]; !ok {
				t.Errorf("%s lists subcommand %q without describing it", path, sub)
			}
		}
	}
	if !slices.Contains(tree.Commands["zensu knowledge"].Aliases, "kb") {
		t.Errorf("zensu knowledge aliases = %q, want kb", tree.Commands["zensu knowledge"].Aliases)
	}
	if !tree.Commands["zensu meta suggest-workflow"].Flags["product"].Required {
		t.Error("zensu meta suggest-workflow --product is marked (required) in its usage")
	}
}

func staleContractMessage(committed, generated []byte) string {
	committed = bytes.ReplaceAll(committed, []byte("\r\n"), []byte("\n"))
	if bytes.Equal(committed, generated) {
		return ""
	}
	line, was, now := firstDifferentLine(string(committed), string(generated))
	return fmt.Sprintf("docs/commands.json does not match the command tree; regenerate it with `make commands` (%s) and commit the result\nfirst difference at line %d:\n  committed: %s\n  generated: %s", generator, line, was, now)
}

func firstDifferentLine(a, b string) (int, string, string) {
	aLines, bLines := strings.Split(a, "\n"), strings.Split(b, "\n")
	for i := range max(len(aLines), len(bLines)) {
		if was, now := lineOrEOF(aLines, i), lineOrEOF(bLines, i); was != now {
			return i + 1, was, now
		}
	}
	return 0, "", ""
}

func lineOrEOF(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "(end of file)"
}

func TestStaleContractMessage_NamesTheRegenerationCommandAndFirstDifference(t *testing.T) {
	generated := []byte("{\n  \"a\": 1,\n  \"b\": 2\n}\n")
	if msg := staleContractMessage(generated, generated); msg != "" {
		t.Errorf("identical files reported stale: %s", msg)
	}
	if msg := staleContractMessage(bytes.ReplaceAll(generated, []byte("\n"), []byte("\r\n")), generated); msg != "" {
		t.Errorf("a CRLF checkout of the same contract reported stale: %s", msg)
	}
	msg := staleContractMessage([]byte("{\n  \"a\": 1,\n  \"b\": 3\n}\n"), generated)
	for _, want := range []string{"make commands", generator, "line 3", `committed:   "b": 3`, `generated:   "b": 2`} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q does not contain %q", msg, want)
		}
	}
	if msg := staleContractMessage(generated[:len(generated)-1], generated); !strings.Contains(msg, "line 5") || !strings.Contains(msg, "(end of file)") {
		t.Errorf("file without its final newline: %q", msg)
	}
}

func TestCommittedContractMatchesTheCommandTree(t *testing.T) {
	generated, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	committed, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "commands.json"))
	if err != nil {
		t.Fatalf("%v; generate it with `make commands` and commit the result", err)
	}
	if msg := staleContractMessage(committed, generated); msg != "" {
		t.Fatal(msg)
	}
}
