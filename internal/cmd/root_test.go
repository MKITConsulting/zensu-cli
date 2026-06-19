package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/MKITConsulting/zensu-cli/internal/version"
)

func TestNewRootCmd_RegistersSubcommands(t *testing.T) {
	root := NewRootCmd()
	want := map[string]bool{"auth": false, "products": false, "features": false, "mocks": false, "design": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("root command missing subcommand %q", name)
		}
	}
}

func TestNewRootCmd_HasAPIURLFlag(t *testing.T) {
	root := NewRootCmd()
	if root.PersistentFlags().Lookup("api-url") == nil {
		t.Error("root should expose persistent --api-url flag")
	}
}

func TestVersionString(t *testing.T) {
	if !strings.Contains(version.String(), version.Version) {
		t.Errorf("version.String() %q should contain Version %q", version.String(), version.Version)
	}
}

func findZshCompletion(root *cobra.Command) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() != "completion" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "zsh" {
				return sub
			}
		}
	}
	return nil
}

func TestNewRootCmd_CompletionZshHelpHasMacOSSetup(t *testing.T) {
	zsh := findZshCompletion(NewRootCmd())
	if zsh == nil {
		t.Fatal("completion zsh subcommand not found")
	}
	for _, want := range []string{"macOS", "compinit", "zcompdump"} {
		if !strings.Contains(zsh.Long, want) {
			t.Errorf("completion zsh help should mention %q", want)
		}
	}
}

func TestNewRootCmd_CompletionStillGenerates(t *testing.T) {
	var buf bytes.Buffer
	if err := NewRootCmd().GenZshCompletion(&buf); err != nil {
		t.Fatalf("GenZshCompletion: %v", err)
	}
	if !strings.Contains(buf.String(), "#compdef") {
		t.Error("generated zsh completion should still contain #compdef")
	}
}
