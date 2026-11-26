package update

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var (
	installScriptKeysLine        = regexp.MustCompile(`(?m)^release_keys\(\) \{\n\techo "([^"]*)"\n\}$`)
	windowsInstallScriptKeysLine = regexp.MustCompile(`(?m)^    function Get-ReleaseKeys \{\r?\n        @\(([^)]*)\)\r?\n    \}\r?$`)
	quotedKey                    = regexp.MustCompile(`'([^']*)'`)
)

func TestInstallScriptTrustsExactlyTheReleaseKeys(t *testing.T) {
	m := installScriptKeysLine.FindSubmatch(readRepoFile(t, "install.sh"))
	if m == nil {
		t.Fatal("install.sh has no release_keys function with one echo line")
	}
	if got := strings.Fields(string(m[1])); !slices.Equal(got, releaseKeys) {
		t.Fatalf("install.sh trusts %q, keys.go %q; keep them equal", got, releaseKeys)
	}
}

func readRepoScript(t *testing.T, name string) []byte {
	t.Helper()
	return bytes.ReplaceAll(readRepoFile(t, name), []byte("\r\n"), []byte("\n"))
}

func TestWindowsInstallScriptTrustsExactlyTheReleaseKeys(t *testing.T) {
	m := windowsInstallScriptKeysLine.FindSubmatch(readRepoScript(t, "install.ps1"))
	if m == nil {
		t.Fatal("install.ps1 has no Get-ReleaseKeys function with one key list")
	}
	var got []string
	for _, q := range quotedKey.FindAllSubmatch(m[1], -1) {
		got = append(got, string(q[1]))
	}
	if !slices.Equal(got, releaseKeys) {
		t.Fatalf("install.ps1 trusts %q, keys.go %q; keep them equal", got, releaseKeys)
	}
}

func TestWindowsInstallScriptSharesTheVerifierSelfTestOfInstallScript(t *testing.T) {
	sh, ps := readRepoScript(t, "install.sh"), readRepoScript(t, "install.ps1")
	value := func(script []byte, name, pattern string) string {
		t.Helper()
		m := regexp.MustCompile(pattern).FindSubmatch(script)
		if m == nil {
			t.Fatalf("no %s found", name)
		}
		return string(m[1])
	}
	key := value(sh, "install.sh selftest_key", `(?m)^selftest_key\(\) \{\n\techo "([^"]*)"\n\}$`)
	sig := value(sh, "install.sh selftest_signature", `(?m)^selftest_signature\(\) \{\n\techo "([^"]*)"\n\}$`)
	if got := value(ps, "install.ps1 Get-SelfTestKey", `(?m)^    function Get-SelfTestKey \{\r?\n        '([^']*)'\r?\n    \}\r?$`); got != key {
		t.Fatalf("install.ps1 tests its verifier with key %q, install.sh with %q", got, key)
	}
	if got := value(ps, "install.ps1 Get-SelfTestSignature", `(?m)^    function Get-SelfTestSignature \{\r?\n        '([^']*)'\r?\n    \}\r?$`); got != sig {
		t.Fatalf("install.ps1 tests its verifier with signature %q, install.sh with %q", got, sig)
	}
	if !bytes.Contains(sh, []byte(`printf 'r' > "${1}/selftest.msg"`)) || !bytes.Contains(ps, []byte(`[IO.File]::WriteAllBytes($message, [byte[]]@(0x72))`)) {
		t.Fatal("install.sh and install.ps1 must both test their verifier on the one-byte message r")
	}
	if !bytes.Contains(sh, []byte(`printf 's' > "${1}/selftest.msg"`)) || !bytes.Contains(ps, []byte(`[IO.File]::WriteAllBytes($message, [byte[]]@(0x73))`)) {
		t.Fatal("install.sh and install.ps1 must both refuse a verifier that also accepts the self-test signature for the message s")
	}
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		t.Fatalf("self-test key %q is not an Ed25519 public key: %v", key, err)
	}
	signature, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || !ed25519.Verify(pub, []byte("r"), signature) {
		t.Fatalf("the self-test signature does not verify the message r under the self-test key: %v", err)
	}
	if ed25519.Verify(pub, []byte("s"), signature) {
		t.Fatal("the self-test signature also verifies another message")
	}
}
