package ciaction

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const publishedReleaseURL = "https://github.com/MKITConsulting/zensu-cli/releases/download"

func publishedTag(t *testing.T) string {
	t.Helper()
	tag := os.Getenv("ZENSU_TEST_PUBLISHED_RELEASE")
	if tag == "" {
		t.Skip("set ZENSU_TEST_PUBLISHED_RELEASE to a published release tag, such as v0.23.0, to check the install scripts against it")
	}
	return tag
}

func downloadPublished(t *testing.T, tag string, names ...string) map[string][]byte {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Minute}
	files := map[string][]byte{}
	for _, name := range names {
		resp, err := client.Get(publishedReleaseURL + "/" + tag + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s of %s: %s", name, tag, resp.Status)
		}
		files[name] = data
	}
	return files
}

func zipEntry(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	t.Fatalf("the archive has no %s", name)
	return nil
}

func tarEntry(t *testing.T, archive []byte, name string) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("the archive has no %s: %v", name, err)
		}
		if h.Name == name {
			data, err := io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
}

type tamperedMirror struct {
	name      string
	tamper    func(files map[string][]byte)
	want      string
	untouched bool
}

func tamperedMirrors(tag, archive string) []tamperedMirror {
	checksums := "zensu_" + strings.TrimPrefix(tag, "v") + "_checksums.txt"
	return []tamperedMirror{
		{name: "unaltered copy", tamper: func(map[string][]byte) {}},
		{name: "altered checksums", untouched: true, want: "error: the signature of " + checksums + " matches no zensu release key, so the download may have been tampered with\n", tamper: func(files map[string][]byte) {
			sum := sha256.Sum256(files[archive])
			hash := hex.EncodeToString(sum[:])
			last := "0"
			if hash[63] == '0' {
				last = "1"
			}
			files[checksums] = bytes.Replace(files[checksums], []byte(hash), []byte(hash[:63]+last), 1)
		}},
		{name: "altered archive", want: "error: checksum mismatch for " + archive + "\n", tamper: func(files map[string][]byte) {
			files[archive] = append(append([]byte(nil), files[archive]...), 0)
		}},
		{name: "removed signature", untouched: true, want: "error: could not download the signature " + checksums + ".sig of release " + tag + "; every zensu release after v0.18.0 is signed, so nothing is installed without it\n", tamper: func(files map[string][]byte) {
			delete(files, checksums+".sig")
		}},
	}
}

func (m tamperedMirror) serve(t *testing.T, tag, archive string, files map[string][]byte) *fakeRelease {
	t.Helper()
	copied := map[string][]byte{}
	for name, data := range files {
		copied[name] = append([]byte(nil), data...)
	}
	m.tamper(copied)
	changed := len(copied) != len(files)
	for name, data := range copied {
		if !bytes.Equal(data, files[name]) {
			changed = true
		}
	}
	if changed != (m.want != "") {
		t.Fatalf("%s: changed the copy %v", m.name, changed)
	}
	return newMirror(t, tag, archive, copied)
}

func (m tamperedMirror) check(t *testing.T, res installRun, mirror *fakeRelease, binary []byte) {
	t.Helper()
	if m.want == "" {
		requirePublishedInstall(t, res, binary, false, "")
		return
	}
	if res.exitCode != 1 || !strings.HasSuffix(res.output, m.want) {
		t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
	}
	if _, err := os.Stat(res.bin); !os.IsNotExist(err) {
		t.Fatalf("a tampered copy was installed: %v", err)
	}
	if m.untouched && mirror.requested(mirror.archive) {
		t.Fatal("the archive was downloaded although its checksums were not verified")
	}
}

func requirePublishedInstall(t *testing.T, res installRun, binary []byte, runnable bool, ver string) {
	t.Helper()
	if res.exitCode != 0 || !strings.Contains(res.output, "Verified the release signature.\n") {
		t.Fatalf("exit %d, output:\n%s", res.exitCode, res.output)
	}
	installed, err := os.ReadFile(res.bin)
	if err != nil || !bytes.Equal(installed, binary) {
		t.Fatalf("installed %d bytes (%v), want the %d bytes of the release binary", len(installed), err, len(binary))
	}
	if !runnable {
		return
	}
	out, err := exec.Command(res.bin, "--version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "zensu "+ver+" ") {
		t.Fatalf("%s --version: %v\n%s", res.bin, err, out)
	}
}

func TestPublishedRelease_InstallPS1VerifiesItAndRefusesTamperedMirrors(t *testing.T) {
	tag := publishedTag(t)
	ver := strings.TrimPrefix(tag, "v")
	archive := windowsArchiveName(ver)
	checksums := "zensu_" + ver + "_checksums.txt"
	files := downloadPublished(t, tag, checksums, checksums+".sig", archive)
	binary := zipEntry(t, files[archive], "zensu.exe")
	script := filepath.Join(repoRoot(t), "install.ps1")
	eachPowerShell(t, func(t *testing.T, shell string) {
		path := pathWithOpenSSL(t)
		direct := runInstallPS1(t, shell, script, map[string]string{"ZENSU_VERSION": tag, "ZENSU_REQUIRE_SIGNATURE": "1", "PATH": path})
		requirePublishedInstall(t, direct, binary, runtime.GOOS == "windows", ver)
		for _, m := range tamperedMirrors(tag, archive) {
			t.Run(m.name, func(t *testing.T) {
				mirror := m.serve(t, tag, archive, files)
				res := runInstallPS1(t, shell, script, map[string]string{"ZENSU_VERSION": tag, "ZENSU_RELEASE_URL": mirror.server.URL, "ZENSU_REQUIRE_SIGNATURE": "1", "PATH": path})
				m.check(t, res, mirror, binary)
			})
		}
	})
}

func TestPublishedRelease_InstallScriptVerifiesItAndRefusesTamperedMirrors(t *testing.T) {
	tag := publishedTag(t)
	requireUnixRunner(t)
	requireOpenSSL3(t)
	ver := strings.TrimPrefix(tag, "v")
	archive := archiveName(ver)
	checksums := "zensu_" + ver + "_checksums.txt"
	files := downloadPublished(t, tag, checksums, checksums+".sig", archive)
	binary := tarEntry(t, files[archive], "zensu")
	script := filepath.Join(repoRoot(t), "install.sh")
	direct := runInstallScript(t, script, map[string]string{"ZENSU_VERSION": tag, "ZENSU_REQUIRE_SIGNATURE": "1"})
	requirePublishedInstall(t, direct, binary, true, ver)
	for _, m := range tamperedMirrors(tag, archive) {
		t.Run(m.name, func(t *testing.T) {
			mirror := m.serve(t, tag, archive, files)
			res := runInstallScript(t, script, map[string]string{"ZENSU_VERSION": tag, "ZENSU_RELEASE_URL": mirror.server.URL, "ZENSU_REQUIRE_SIGNATURE": "1"})
			m.check(t, res, mirror, binary)
		})
	}
}
