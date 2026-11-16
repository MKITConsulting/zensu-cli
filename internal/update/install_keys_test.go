package update

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

var installScriptKeysLine = regexp.MustCompile(`(?m)^release_keys\(\) \{\n\techo "([^"]*)"\n\}$`)

func TestInstallScriptTrustsExactlyTheReleaseKeys(t *testing.T) {
	m := installScriptKeysLine.FindSubmatch(readRepoFile(t, "install.sh"))
	if m == nil {
		t.Fatal("install.sh has no release_keys function with one echo line")
	}
	if got := strings.Fields(string(m[1])); !slices.Equal(got, releaseKeys) {
		t.Fatalf("install.sh trusts %q, keys.go %q; keep them equal", got, releaseKeys)
	}
}
