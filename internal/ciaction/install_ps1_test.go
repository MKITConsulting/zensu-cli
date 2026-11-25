package ciaction

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func installPS1Fixture(t *testing.T) (signingKey, *fakeRelease, string) {
	t.Helper()
	key := newSigningKey(t)
	release := newWindowsRelease(t, installTag, fakeBinary, key)
	return key, release, patchedInstallPS1(t, t.TempDir(), key.public)
}

func pathWithOpenSSL(t *testing.T) string {
	t.Helper()
	return filepath.Dir(requireOpenSSL3(t)) + string(os.PathListSeparator) + os.Getenv("PATH")
}

func fakeVerifier(t *testing.T, unix, windows string) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "openssl.cmd")
		if err := os.WriteFile(path, []byte(windows), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	path := filepath.Join(dir, "openssl")
	if err := os.WriteFile(path, []byte(unix), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func runPowerShellCommand(t *testing.T, shell, command string, env map[string]string) (string, int) {
	t.Helper()
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.Env = powerShellEnv(t, env)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running %s: %v", shell, err)
	}
	return strings.ReplaceAll(string(out), "\r\n", "\n"), code
}

func TestInstallPS1_InstallsAReleaseWhoseSignatureMatches(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		res := runInstallPS1(t, shell, script, map[string]string{
			"ZENSU_VERSION":           strings.TrimPrefix(installTag, "v"),
			"ZENSU_RELEASE_URL":       release.server.URL + "/",
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"PATH":                    pathWithOpenSSL(t),
		})
		if res.exitCode != 0 || !res.installed(t) {
			t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
		}
		if !strings.Contains(res.output, "Downloading "+release.archive+" ("+installTag+") ...\nVerified the release signature.\n") {
			t.Fatalf("output:\n%s", res.output)
		}
		want := []string{"/" + installTag + "/" + release.checksums, "/" + installTag + "/" + release.checksums + ".sig", "/" + installTag + "/" + release.archive}
		if got := release.requestLog(); !slices.Equal(got, want) {
			t.Fatalf("requests %v, want the checksums and their signature before the archive %v", got, want)
		}
	})
}

func TestInstallPS1_AcceptsAnyValidSignatureLine(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		key, release, script := installPS1Fixture(t)
		other := newSigningKey(t)
		checksums := release.read(release.checksums)
		release.write(release.checksums+".sig", []byte("not-base64!\r\n"+other.sign(checksums)+"\r\n\r\n"+key.sign(checksums)+"\r\n"))
		res := runInstallPS1(t, shell, script, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"PATH":                    pathWithOpenSSL(t),
		})
		if res.exitCode != 0 || !res.installed(t) || !strings.Contains(res.output, "Verified the release signature.\n") {
			t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
		}
	})
}

func TestInstallPS1_TrustsEveryEmbeddedKey(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		old, current := newSigningKey(t), newSigningKey(t)
		release := newWindowsRelease(t, installTag, fakeBinary, current)
		script := patchedInstallPS1(t, t.TempDir(), old.public, current.public)
		res := runInstallPS1(t, shell, script, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"PATH":                    pathWithOpenSSL(t),
		})
		if res.exitCode != 0 || !res.installed(t) {
			t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
		}
	})
}

func TestInstallPS1_UsesTheOpenSSLThatZensuOpensslNames(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		libressl := fakeVerifier(t, "#!/bin/sh\necho 'LibreSSL 3.3.6'\n", "@echo off\r\necho LibreSSL 3.3.6\r\n")
		res := runInstallPS1(t, shell, script, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"ZENSU_OPENSSL":           requireOpenSSL3(t),
			"PATH":                    filepath.Dir(libressl) + string(os.PathListSeparator) + os.Getenv("PATH"),
		})
		if res.exitCode != 0 || !res.installed(t) || !strings.Contains(res.output, "Verified the release signature.\n") {
			t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
		}
	})
}

func TestInstallPS1_AWrongSignatureAlwaysFails(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		for _, require := range []string{"1", ""} {
			_, release, script := installPS1Fixture(t)
			forged := newSigningKey(t)
			release.write(release.checksums+".sig", []byte(forged.sign(release.read(release.checksums))+"\n"))
			res := runInstallPS1(t, shell, script, map[string]string{
				"ZENSU_VERSION":           installTag,
				"ZENSU_RELEASE_URL":       release.server.URL,
				"ZENSU_REQUIRE_SIGNATURE": require,
				"PATH":                    pathWithOpenSSL(t),
			})
			want := "error: the signature of zensu_9.8.7_checksums.txt matches no zensu release key, so the download may have been tampered with\n"
			if res.exitCode != 1 || !strings.HasSuffix(res.output, want) || res.installed(t) {
				t.Fatalf("require=%q: exit %d, output:\n%s", require, res.exitCode, res.output)
			}
			if release.requested(release.archive) {
				t.Fatalf("require=%q: the archive was downloaded before the signature was verified", require)
			}
		}
	})
}

func TestInstallPS1_RefusesAnArchiveThatDiffersFromTheSignedChecksums(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		release.write(release.archive, zipball(t, []byte("#!/bin/sh\necho replaced\n")))
		res := runInstallPS1(t, shell, script, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"PATH":                    pathWithOpenSSL(t),
		})
		if res.exitCode != 1 || !strings.HasSuffix(res.output, "error: checksum mismatch for "+release.archive+"\n") {
			t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
		}
		if _, err := os.Stat(res.bin); !os.IsNotExist(err) {
			t.Fatal("a mismatching archive was installed")
		}
	})
}

func TestInstallPS1_AMissingSignatureOfASignedReleaseAlwaysFails(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		release.remove(release.checksums + ".sig")
		for _, require := range []string{"1", "", "0"} {
			res := runInstallPS1(t, shell, script, map[string]string{
				"ZENSU_VERSION":           installTag,
				"ZENSU_RELEASE_URL":       release.server.URL,
				"ZENSU_REQUIRE_SIGNATURE": require,
			})
			want := "error: could not download the signature zensu_9.8.7_checksums.txt.sig of release " + installTag + "; every zensu release after v0.18.0 is signed, so nothing is installed without it\n"
			if res.exitCode != 1 || !strings.HasSuffix(res.output, want) || res.installed(t) {
				t.Fatalf("require=%q: exit %d, output:\n%s", require, res.exitCode, res.output)
			}
		}
		if release.requested(release.archive) {
			t.Fatal("the archive of a release without its signature was downloaded")
		}
	})
}

func TestInstallPS1_AMissingSignatureOfAnUnsignedReleaseFailsOnlyWhenRequired(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		release := newWindowsRelease(t, "v0.18.0", fakeBinary, newSigningKey(t))
		release.remove(release.checksums + ".sig")
		script := patchedInstallPS1(t, t.TempDir(), newSigningKey(t).public)
		env := map[string]string{"ZENSU_VERSION": "0.18.0", "ZENSU_RELEASE_URL": release.server.URL}

		for _, require := range []string{"1", "yes"} {
			env["ZENSU_REQUIRE_SIGNATURE"] = require
			required := runInstallPS1(t, shell, script, env)
			if required.exitCode != 1 || !strings.HasSuffix(required.output, "error: release v0.18.0 carries no signature (zensu_0.18.0_checksums.txt.sig)\n") || required.installed(t) {
				t.Fatalf("require=%q: exit %d, output:\n%s", require, required.exitCode, required.output)
			}
		}

		for _, require := range []string{"", "0"} {
			env["ZENSU_REQUIRE_SIGNATURE"] = require
			optional := runInstallPS1(t, shell, script, env)
			if optional.exitCode != 0 || !optional.installed(t) || !strings.Contains(optional.output, "Note: release v0.18.0 carries no signature, so only its checksums are checked.\n") {
				t.Fatalf("require=%q: exit %d, output:\n%s", require, optional.exitCode, optional.output)
			}
		}
	})
}

func TestInstallPS1_AMissingVerifierFailsOnlyWhenRequired(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		for name, verifier := range map[string]string{
			"libressl": fakeVerifier(t, "#!/bin/sh\necho 'LibreSSL 3.3.6'\n", "@echo off\r\necho LibreSSL 3.3.6\r\n"),
			"openssl3 no ed25519": fakeVerifier(t,
				"#!/bin/sh\ncase \"$1\" in\nversion) echo 'OpenSSL 3.0.13 30 Jan 2024' ;;\n*) exit 1 ;;\nesac\n",
				"@echo off\r\nif \"%~1\"==\"version\" (\r\n  echo OpenSSL 3.0.13 30 Jan 2024\r\n  exit /b 0\r\n)\r\nexit /b 1\r\n"),
			"missing path": filepath.Join(t.TempDir(), "absent", "openssl"),
		} {
			_, release, script := installPS1Fixture(t)
			env := map[string]string{"ZENSU_VERSION": installTag, "ZENSU_RELEASE_URL": release.server.URL, "ZENSU_OPENSSL": verifier}

			env["ZENSU_REQUIRE_SIGNATURE"] = "1"
			required := runInstallPS1(t, shell, script, env)
			if required.exitCode != 1 || !strings.HasSuffix(required.output, "error: verifying the release signature needs OpenSSL 3 or later with Ed25519 support; install it or set ZENSU_OPENSSL to its path\n") || required.installed(t) {
				t.Fatalf("%s required: exit %d, output:\n%s", name, required.exitCode, required.output)
			}

			env["ZENSU_REQUIRE_SIGNATURE"] = "0"
			optional := runInstallPS1(t, shell, script, env)
			if optional.exitCode != 0 || !optional.installed(t) || !strings.Contains(optional.output, "Note: OpenSSL 3 or later with Ed25519 support was not found, so the release signature was not checked; set ZENSU_REQUIRE_SIGNATURE=1 to require it.\n") {
				t.Fatalf("%s optional: exit %d, output:\n%s", name, optional.exitCode, optional.output)
			}
		}
	})
}

func TestInstallPS1_RefusesUnusableVersionsAndMirrors(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		for _, tc := range []struct {
			env  map[string]string
			want string
		}{
			{map[string]string{"ZENSU_RELEASE_URL": release.server.URL}, "error: ZENSU_RELEASE_URL needs ZENSU_VERSION, because a mirror cannot resolve the latest release\n"},
			{map[string]string{"ZENSU_VERSION": "1.0;touch pwned", "ZENSU_RELEASE_URL": release.server.URL}, "error: invalid version v1.0;touch pwned\n"},
			{map[string]string{"ZENSU_VERSION": "v1.2.3", "ZENSU_RELEASE_URL": release.server.URL}, "error: could not download " + release.server.URL + "/v1.2.3/zensu_1.2.3_checksums.txt\n"},
		} {
			res := runInstallPS1(t, shell, script, tc.env)
			if res.exitCode != 1 || !strings.HasSuffix(res.output, tc.want) || res.installed(t) {
				t.Errorf("%v: exit %d, output:\n%s", tc.env, res.exitCode, res.output)
			}
		}
	})
}

func TestInstallPS1_InvokeExpressionKeepsTheCallersSession(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		command := "$ErrorActionPreference = 'Continue'; try { Invoke-Expression ([IO.File]::ReadAllText(" + psQuote(script) + ")) } catch { Write-Output ('caught: ' + $_.Exception.Message) }; Write-Output 'session survived'"

		bin := filepath.Join(t.TempDir(), "bin")
		failed, code := runPowerShellCommand(t, shell, command, map[string]string{
			"ZENSU_VERSION":        "1.0;touch pwned",
			"ZENSU_RELEASE_URL":    release.server.URL,
			"ZENSU_INSTALL_DIR":    bin,
			"ZENSU_NO_MODIFY_PATH": "1",
		})
		if code != 0 || !strings.HasSuffix(failed, "caught: invalid version v1.0;touch pwned\nsession survived\n") || strings.Contains(failed, "error: invalid version") {
			t.Fatalf("exit %d, output:\n%s", code, failed)
		}

		installed, code := runPowerShellCommand(t, shell, command, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"ZENSU_INSTALL_DIR":       bin,
			"ZENSU_NO_MODIFY_PATH":    "1",
			"PATH":                    pathWithOpenSSL(t),
		})
		if code != 0 || !strings.Contains(installed, "Verified the release signature.\n") || !strings.HasSuffix(installed, "session survived\n") || strings.Contains(installed, "caught:") {
			t.Fatalf("exit %d, output:\n%s", code, installed)
		}
		if data, err := os.ReadFile(filepath.Join(bin, "zensu.exe")); err != nil || string(data) != string(fakeBinary) {
			t.Fatalf("installed %q, %v", data, err)
		}
	})
}

func TestInstallPS1_LeavesThePathAloneWhenAsked(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		env := map[string]string{"ZENSU_VERSION": installTag, "ZENSU_RELEASE_URL": release.server.URL, "PATH": pathWithOpenSSL(t)}
		if runtime.GOOS == "windows" {
			userPath := func() string {
				out, code := runPowerShellCommand(t, shell, "[Environment]::GetEnvironmentVariable('Path', 'User')", nil)
				if code != 0 {
					t.Fatalf("reading the user PATH: exit %d\n%s", code, out)
				}
				return out
			}
			before := userPath()
			env["ZENSU_NO_MODIFY_PATH"] = "1"
			res := runInstallPS1(t, shell, script, env)
			if after := userPath(); after != before {
				t.Errorf("the user PATH changed from %q to %q", before, after)
			}
			if res.exitCode != 0 || !res.installed(t) || strings.Contains(res.output, "to your user PATH") {
				t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
			}
			return
		}
		for value, modifies := range map[string]bool{"1": false, "yes": false, "0": true, "": true} {
			env["ZENSU_NO_MODIFY_PATH"] = value
			res := runInstallPS1(t, shell, script, env)
			note := "Note: add " + filepath.Dir(res.bin) + " to your PATH.\n"
			if res.exitCode != 0 || !res.installed(t) || strings.Contains(res.output, note) != modifies {
				t.Errorf("ZENSU_NO_MODIFY_PATH=%q: exit %d, output:\n%s", value, res.exitCode, res.output)
			}
		}
	})
}

func TestInstallPS1_FindsTheOpenSSLOfGitForWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Git for Windows bundles its OpenSSL only on Windows")
	}
	programFiles := os.Getenv("ProgramFiles")
	bundled := ""
	for _, path := range []string{filepath.Join(programFiles, "Git", "mingw64", "bin", "openssl.exe"), filepath.Join(programFiles, "Git", "usr", "bin", "openssl.exe")} {
		if _, err := os.Stat(path); err == nil {
			bundled = path
			break
		}
	}
	if bundled == "" {
		if os.Getenv("CI") != "" {
			t.Fatal("the Windows CI runner has no Git for Windows OpenSSL")
		}
		t.Skip("no Git for Windows OpenSSL on this machine")
	}
	var dirs []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "openssl.exe")); err == nil {
			continue
		}
		dirs = append(dirs, dir)
	}
	eachPowerShell(t, func(t *testing.T, shell string) {
		_, release, script := installPS1Fixture(t)
		res := runInstallPS1(t, shell, script, map[string]string{
			"ZENSU_VERSION":           installTag,
			"ZENSU_RELEASE_URL":       release.server.URL,
			"ZENSU_REQUIRE_SIGNATURE": "1",
			"PATH":                    strings.Join(dirs, string(os.PathListSeparator)),
		})
		if res.exitCode != 0 || !res.installed(t) || !strings.Contains(res.output, "Verified the release signature.\n") {
			t.Fatalf("with %s bundled and no openssl on PATH: exit %d, output:\n%s", bundled, res.exitCode, res.output)
		}
	})
}

func TestInstallPS1_ScriptParses(t *testing.T) {
	eachPowerShell(t, func(t *testing.T, shell string) {
		path := filepath.Join(repoRoot(t), "install.ps1")
		out, code := runPowerShellCommand(t, shell, "$errors = $null; [void][System.Management.Automation.Language.Parser]::ParseFile("+psQuote(path)+", [ref]$null, [ref]$errors); $errors | ForEach-Object { $_.ToString() }; Write-Output ('errors: ' + @($errors).Count)", nil)
		if code != 0 || !strings.HasSuffix(out, "errors: 0\n") {
			t.Fatalf("exit %d, output:\n%s", code, out)
		}
	})
}
