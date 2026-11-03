package cmd

import (
	"slices"
	"testing"

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
