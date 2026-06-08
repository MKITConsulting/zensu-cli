package cmd

import (
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/version"
)

func TestNewRootCmd_RegistersSubcommands(t *testing.T) {
	root := NewRootCmd()
	want := map[string]bool{"auth": false, "api": false, "products": false, "features": false}
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
