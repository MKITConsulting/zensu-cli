package cmd

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestIsRequiredFlag_ReadsTheUsageMarkerAndCobrasAnnotation(t *testing.T) {
	for _, tc := range []struct {
		usage string
		want  bool
	}{
		{"product ID (required)", true},
		{"source file as path[:type[:language]] (repeatable, required)", true},
		{"repositories this worker can check out (repeatable; required)", true},
		{"parent feature ID (required; or pass as positional arg)", false},
		{"the option the session continues with (required unless --blocking)", false},
		{"block reason, prose only (required with --blocked)", false},
		{"whether authentication is required", false},
		{"threat model status (not-required|pending|completed)", false},
		{"", false},
	} {
		if got := IsRequiredFlag(&pflag.Flag{Name: "x", Usage: tc.usage}); got != tc.want {
			t.Errorf("IsRequiredFlag(usage %q) = %v, want %v", tc.usage, got, tc.want)
		}
	}

	cmd := &cobra.Command{Use: "things"}
	cmd.Flags().String("name", "", "name of the thing")
	if IsRequiredFlag(cmd.Flags().Lookup("name")) {
		t.Fatal("a flag without marker or annotation is not required")
	}
	if err := cmd.MarkFlagRequired("name"); err != nil {
		t.Fatal(err)
	}
	if !IsRequiredFlag(cmd.Flags().Lookup("name")) {
		t.Error("a flag marked with cobra's MarkFlagRequired is required")
	}
}

func TestIsRequiredFlag_MarksFlagsTheirCommandRejectsWhenMissing(t *testing.T) {
	aiWorkTestRepo(t)
	const id = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	const event = "891419fa-f7e4-4a52-8123-2bef791876dd"
	errReached := errors.New("reached the API client")
	newRoot := func() *cobra.Command {
		f := &Factory{Out: io.Discard, NewClient: func(context.Context) (*client.Client, error) { return nil, errReached }}
		root := &cobra.Command{Use: "zensu", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(NewAIWorkCmd(f), NewKnowledgeCmd(f))
		return root
	}
	startArgs := []string{"ai-work", "start", "--id", id, "--title", "Goal", "--maintenance-reason", "Upgrade tooling", "--product", id}
	submitArgs := []string{"ai-work", "submit", id, "--event-id", event, "--expected-version", "1", "--artifact", "commit:abc", "--criteria-version", "sha256:def"}
	recoverArgs := []string{"ai-work", "recover", "--session", "scv1_" + strings.Repeat("a", 64)}
	bindArgs := []string{"ai-work", "bind", "--product", id, "--confirm"}
	searchArgs := []string{"knowledge", "context-search", "--query", "Fix login"}
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{startArgs, "id"}, {startArgs, "title"},
		{submitArgs, "event-id"}, {submitArgs, "expected-version"}, {submitArgs, "artifact"}, {submitArgs, "criteria-version"},
		{recoverArgs, "session"}, {bindArgs, "confirm"}, {searchArgs, "query"},
	} {
		t.Run(strings.Join(tc.args[:2], " ")+" --"+tc.flag, func(t *testing.T) {
			if err := runCmd(t, newRoot(), tc.args...); err != nil && !errors.Is(err, errReached) {
				t.Fatalf("the complete invocation is rejected before the API client: %v", err)
			}
			root := newRoot()
			cmd, _ := resolveCommand(root, tc.args)
			flag := cmd.Flags().Lookup(tc.flag)
			if !IsRequiredFlag(flag) {
				t.Errorf("%s rejects a missing --%s, but its usage %q carries no required marker", cmd.CommandPath(), tc.flag, flag.Usage)
			}
			at, width := slices.Index(tc.args, "--"+tc.flag), 2
			if flag.NoOptDefVal != "" {
				width = 1
			}
			omitted := slices.Delete(slices.Clone(tc.args), at, at+width)
			if err := runCmd(t, root, omitted...); err == nil || errors.Is(err, errReached) {
				t.Errorf("%s without --%s is not rejected before the API client: %v", cmd.CommandPath(), tc.flag, err)
			}
		})
	}
}

func TestVisitCommands_WalksTheCommandAndEveryDescendantInPreOrder(t *testing.T) {
	root := &cobra.Command{Use: "zensu"}
	beta := &cobra.Command{Use: "beta"}
	alpha := &cobra.Command{Use: "alpha"}
	alpha.AddCommand(&cobra.Command{Use: "two"}, &cobra.Command{Use: "one"})
	root.AddCommand(beta, alpha)

	var visited []string
	VisitCommands(root, func(c *cobra.Command) {
		visited = append(visited, c.CommandPath())
	})
	want := []string{"zensu", "zensu alpha", "zensu alpha one", "zensu alpha two", "zensu beta"}
	if !slices.Equal(visited, want) {
		t.Errorf("visited %q, want %q", visited, want)
	}
}

func TestPlaceholderArg_SatisfiesTheTreesContentValidators(t *testing.T) {
	for name, validate := range map[string]cobra.PositionalArgs{
		"aiWorkIDArgs":       aiWorkIDArgs,
		"pulseSessionIDArgs": pulseSessionIDArgs,
	} {
		if err := validate(&cobra.Command{Use: "probe"}, []string{PlaceholderArg}); err != nil {
			t.Errorf("%s rejects PlaceholderArg: %v", name, err)
		}
	}
}
