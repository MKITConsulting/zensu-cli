package ciaction

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	installTag    = "v9.8.7"
	unixDownloads = "https://github.com/${REPO}/releases/download"
)

var fakeBinary = []byte("#!/bin/sh\necho zensu 9.8.7\n")

type installRun struct {
	output   string
	exitCode int
	bin      string
}

func runInstallScript(t *testing.T, script string, env map[string]string) installRun {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	vars := map[string]string{"ZENSU_INSTALL_DIR": bin}
	for k, v := range env {
		vars[k] = v
	}
	cmd := exec.Command("sh", script)
	cmd.Env = runnerEnv(t, vars)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running install.sh: %v", err)
	}
	return installRun{output: string(out), exitCode: code, bin: filepath.Join(bin, "zensu")}
}

func (r installRun) installed(t *testing.T) bool {
	t.Helper()
	data, err := os.ReadFile(r.bin)
	if err != nil {
		return false
	}
	if string(data) != string(fakeBinary) {
		t.Fatalf("installed %q, want the release binary", data)
	}
	return true
}

func installFixture(t *testing.T) (signingKey, *fakeRelease, string) {
	t.Helper()
	requireUnixRunner(t)
	key := newSigningKey(t)
	release := newFakeRelease(t, installTag, fakeBinary, key)
	return key, release, patchedInstallScript(t, t.TempDir(), key.public)
}

func TestActionInstall_InstallsAReleaseWhoseSignatureMatches(t *testing.T) {
	requireOpenSSL3(t)
	_, release, script := installFixture(t)
	res := runInstallScript(t, script, map[string]string{
		"ZENSU_VERSION":           strings.TrimPrefix(installTag, "v"),
		"ZENSU_RELEASE_URL":       release.server.URL + "/",
		"ZENSU_REQUIRE_SIGNATURE": "1",
	})
	if res.exitCode != 0 || !res.installed(t) {
		t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
	}
	if !strings.Contains(res.output, "Downloading "+release.archive+" ("+installTag+") ...\nVerified the release signature.\n") {
		t.Fatalf("output:\n%s", res.output)
	}
}

func TestActionInstall_InstallsTheLatestReleaseWhenNoVersionIsSet(t *testing.T) {
	requireOpenSSL3(t)
	_, release, script := installFixture(t)
	script = pointedAt(t, script, release, unixDownloads)
	for _, version := range []string{"", "latest"} {
		res := runInstallScript(t, script, map[string]string{"ZENSU_VERSION": version, "ZENSU_REQUIRE_SIGNATURE": "1"})
		if res.exitCode != 0 || !res.installed(t) || !strings.Contains(res.output, "Downloading "+release.archive+" ("+installTag+") ...\nVerified the release signature.\n") {
			t.Fatalf("ZENSU_VERSION=%q: exit %d, output:\n%s", version, res.exitCode, res.output)
		}
	}
	if got := release.requestLog(); len(got) == 0 || got[0] != latestReleasePath {
		t.Fatalf("requests %v, want the latest release looked up first", got)
	}
}

func TestActionInstall_AcceptsAnyValidSignatureLine(t *testing.T) {
	requireOpenSSL3(t)
	key, release, script := installFixture(t)
	other := newSigningKey(t)
	checksums := release.read(release.checksums)
	release.write(release.checksums+".sig", []byte("not-base64!\r\n"+other.sign(checksums)+"\r\n\r\n"+key.sign(checksums)+"\r\n"))
	res := runInstallScript(t, script, map[string]string{
		"ZENSU_VERSION":           installTag,
		"ZENSU_RELEASE_URL":       release.server.URL,
		"ZENSU_REQUIRE_SIGNATURE": "1",
	})
	if res.exitCode != 0 || !res.installed(t) {
		t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
	}
}

func TestActionInstall_TrustsEveryEmbeddedKey(t *testing.T) {
	requireOpenSSL3(t)
	requireUnixRunner(t)
	old, current := newSigningKey(t), newSigningKey(t)
	release := newFakeRelease(t, installTag, fakeBinary, current)
	script := patchedInstallScript(t, t.TempDir(), old.public, current.public)
	res := runInstallScript(t, script, map[string]string{
		"ZENSU_VERSION":           installTag,
		"ZENSU_RELEASE_URL":       release.server.URL,
		"ZENSU_REQUIRE_SIGNATURE": "1",
	})
	if res.exitCode != 0 || !res.installed(t) {
		t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
	}
}

func TestActionInstall_AWrongSignatureAlwaysFails(t *testing.T) {
	requireOpenSSL3(t)
	for _, require := range []string{"1", ""} {
		_, release, script := installFixture(t)
		forged := newSigningKey(t)
		release.write(release.checksums+".sig", []byte(forged.sign(release.read(release.checksums))+"\n"))
		res := runInstallScript(t, script, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": require,
		})
		want := "error: the signature of zensu_9.8.7_checksums.txt matches no zensu release key, so the download may have been tampered with\n"
		if res.exitCode != 1 || !strings.HasSuffix(res.output, want) || res.installed(t) {
			t.Fatalf("require=%q: exit %d, output:\n%s", require, res.exitCode, res.output)
		}
	}
}

func TestActionInstall_RefusesAnArchiveThatDiffersFromTheSignedChecksums(t *testing.T) {
	requireOpenSSL3(t)
	_, release, script := installFixture(t)
	release.write(release.archive, tarball(t, []byte("#!/bin/sh\necho replaced\n")))
	res := runInstallScript(t, script, map[string]string{
		"ZENSU_VERSION":           installTag,
		"ZENSU_RELEASE_URL":       release.server.URL,
		"ZENSU_REQUIRE_SIGNATURE": "1",
	})
	if res.exitCode != 1 || !strings.HasSuffix(res.output, "error: checksum mismatch for "+release.archive+"\n") {
		t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
	}
	if _, err := os.Stat(res.bin); !os.IsNotExist(err) {
		t.Fatal("a mismatching archive was installed")
	}
}

func TestActionInstall_AMissingSignatureOfASignedReleaseAlwaysFails(t *testing.T) {
	requireUnixRunner(t)
	for _, tc := range []struct {
		tag      string
		requires []string
	}{
		{installTag, []string{"1", "", "0"}},
		{"v0.18.1", []string{""}},
		{"v0.18.0-rc.1", []string{""}},
		{"v0.5.01", []string{""}},
	} {
		release := newFakeRelease(t, tc.tag, fakeBinary, newSigningKey(t))
		release.remove(release.checksums + ".sig")
		script := patchedInstallScript(t, t.TempDir(), newSigningKey(t).public)
		for _, require := range tc.requires {
			res := runInstallScript(t, script, map[string]string{
				"ZENSU_VERSION":           tc.tag,
				"ZENSU_RELEASE_URL":       release.server.URL,
				"ZENSU_REQUIRE_SIGNATURE": require,
			})
			want := "error: could not download the signature " + release.checksums + ".sig of release " + tc.tag + "; every zensu release after v0.18.0 is signed, so nothing is installed without it\n"
			if res.exitCode != 1 || !strings.HasSuffix(res.output, want) || res.installed(t) {
				t.Fatalf("%s require=%q: exit %d, output:\n%s", tc.tag, require, res.exitCode, res.output)
			}
		}
		if release.requested(release.archive) {
			t.Fatalf("the archive of %s was downloaded without its signature", tc.tag)
		}
	}
}

func TestActionInstall_AnUnsignedLatestReleaseAlwaysFails(t *testing.T) {
	requireUnixRunner(t)
	release := newFakeRelease(t, "v0.18.0", fakeBinary, newSigningKey(t))
	release.remove(release.checksums + ".sig")
	script := pointedAt(t, patchedInstallScript(t, t.TempDir(), newSigningKey(t).public), release, unixDownloads)
	for _, require := range []string{"", "0", "1"} {
		res := runInstallScript(t, script, map[string]string{"ZENSU_REQUIRE_SIGNATURE": require})
		want := "error: could not download the signature zensu_0.18.0_checksums.txt.sig of the latest release v0.18.0; the latest zensu release is always signed, so nothing is installed without it\n"
		if res.exitCode != 1 || !strings.HasSuffix(res.output, want) || res.installed(t) {
			t.Fatalf("require=%q: exit %d, output:\n%s", require, res.exitCode, res.output)
		}
	}
	if release.requested(release.archive) {
		t.Fatal("the archive of an unsigned latest release was downloaded")
	}
}

func TestActionInstall_AMissingSignatureOfAnUnsignedReleaseFailsOnlyWhenRequired(t *testing.T) {
	requireUnixRunner(t)
	for _, version := range []string{"0.18.0", "v0.14.1"} {
		tag := "v" + strings.TrimPrefix(version, "v")
		release := newFakeRelease(t, tag, fakeBinary, newSigningKey(t))
		release.remove(release.checksums + ".sig")
		script := patchedInstallScript(t, t.TempDir(), newSigningKey(t).public)
		env := map[string]string{"ZENSU_VERSION": version, "ZENSU_RELEASE_URL": release.server.URL}

		for _, require := range []string{"1", "yes"} {
			env["ZENSU_REQUIRE_SIGNATURE"] = require
			required := runInstallScript(t, script, env)
			if required.exitCode != 1 || !strings.HasSuffix(required.output, "error: release "+tag+" carries no signature ("+release.checksums+".sig)\n") || required.installed(t) {
				t.Fatalf("%s require=%q: exit %d, output:\n%s", tag, require, required.exitCode, required.output)
			}
		}

		for _, require := range []string{"", "0"} {
			env["ZENSU_REQUIRE_SIGNATURE"] = require
			optional := runInstallScript(t, script, env)
			if optional.exitCode != 0 || !optional.installed(t) || !strings.Contains(optional.output, "Note: release "+tag+" carries no signature, so only its checksums are checked.\n") {
				t.Fatalf("%s require=%q: exit %d, output:\n%s", tag, require, optional.exitCode, optional.output)
			}
		}
	}
}

func TestActionInstall_AMissingVerifierFailsOnlyWhenRequired(t *testing.T) {
	for name, fake := range map[string]string{
		"libressl":                "#!/bin/sh\necho 'LibreSSL 3.3.6'\n",
		"openssl3 no ed25519":     "#!/bin/sh\ncase \"$1\" in\nversion) echo 'OpenSSL 3.0.13 30 Jan 2024' ;;\nbase64) cat >/dev/null; printf x ;;\n*) exit 1 ;;\nesac\n",
		"accepts every signature": "#!/bin/sh\ncase \"$1\" in\nversion) echo 'OpenSSL 3.0.13 30 Jan 2024' ;;\nbase64) cat >/dev/null; printf x ;;\nesac\nexit 0\n",
	} {
		_, release, script := installFixture(t)
		verifier := filepath.Join(t.TempDir(), "openssl")
		if err := os.WriteFile(verifier, []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{"ZENSU_VERSION": installTag, "ZENSU_RELEASE_URL": release.server.URL, "ZENSU_OPENSSL": verifier}

		env["ZENSU_REQUIRE_SIGNATURE"] = "1"
		required := runInstallScript(t, script, env)
		if required.exitCode != 1 || !strings.HasSuffix(required.output, "error: verifying the release signature needs OpenSSL 3 or later with Ed25519 support; install it or set ZENSU_OPENSSL to its path\n") || required.installed(t) {
			t.Fatalf("%s required: exit %d, output:\n%s", name, required.exitCode, required.output)
		}

		env["ZENSU_REQUIRE_SIGNATURE"] = "0"
		optional := runInstallScript(t, script, env)
		if optional.exitCode != 0 || !optional.installed(t) || !strings.Contains(optional.output, "Note: OpenSSL 3 or later with Ed25519 support was not found, so the release signature was not checked; set ZENSU_REQUIRE_SIGNATURE=1 to require it.\n") {
			t.Fatalf("%s optional: exit %d, output:\n%s", name, optional.exitCode, optional.output)
		}
	}
}

func TestActionInstall_RefusesUnusableVersionsAndMirrors(t *testing.T) {
	_, release, script := installFixture(t)
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"ZENSU_RELEASE_URL": release.server.URL}, "error: ZENSU_RELEASE_URL needs ZENSU_VERSION, because a mirror cannot resolve the latest release\n"},
		{map[string]string{"ZENSU_VERSION": "1.0;touch pwned", "ZENSU_RELEASE_URL": release.server.URL}, "error: invalid version v1.0;touch pwned\n"},
		{map[string]string{"ZENSU_VERSION": "v1.2.3", "ZENSU_RELEASE_URL": release.server.URL}, "error: could not download " + release.server.URL + "/v1.2.3/zensu_1.2.3_checksums.txt\n"},
	} {
		res := runInstallScript(t, script, tc.env)
		if res.exitCode != 1 || !strings.HasSuffix(res.output, tc.want) || res.installed(t) {
			t.Errorf("%v: exit %d, output:\n%s", tc.env, res.exitCode, res.output)
		}
	}
}

func TestActionInstall_ScriptIsValidPOSIXShell(t *testing.T) {
	requireUnixRunner(t)
	if out, err := exec.Command("sh", "-n", filepath.Join(repoRoot(t), "install.sh")).CombinedOutput(); err != nil {
		t.Fatalf("sh -n install.sh: %v\n%s", err, out)
	}
}
