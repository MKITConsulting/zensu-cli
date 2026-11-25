package ciaction

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	releaseKeysLine        = regexp.MustCompile(`(?m)^release_keys\(\) \{\n\techo "([^"]*)"\n\}$`)
	windowsReleaseKeysLine = regexp.MustCompile(`(?m)^function Get-ReleaseKeys \{\r?\n    @\(([^)]*)\)\r?\n\}\r?$`)
)

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func requireUnixRunner(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh and the coverage action run on Linux and macOS")
	}
	for _, tool := range []string{"sh", "bash", "tar", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is needed: %v", tool, err)
		}
	}
}

func requireOpenSSL3(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{"openssl", "/opt/homebrew/opt/openssl@3/bin/openssl", "/usr/local/opt/openssl@3/bin/openssl"} {
		path, err := exec.LookPath(candidate)
		if err != nil {
			continue
		}
		out, err := exec.Command(path, "version").Output()
		if err == nil && regexp.MustCompile(`^OpenSSL ([3-9]\.|[1-9][0-9])`).Match(out) {
			return path
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("verifying release signatures needs OpenSSL 3 or later on the CI runner")
	}
	t.Skip("no OpenSSL 3 or later on this machine")
	return ""
}

func powerShells(t *testing.T) []string {
	t.Helper()
	names := []string{"pwsh"}
	if runtime.GOOS == "windows" {
		names = append(names, "powershell")
	}
	var shells []string
	for _, name := range names {
		if path, err := exec.LookPath(name); err == nil {
			shells = append(shells, path)
		}
	}
	if len(shells) == 0 {
		if os.Getenv("CI") != "" {
			t.Fatal("testing install.ps1 needs PowerShell on the CI runner")
		}
		t.Skip("no PowerShell on this machine")
	}
	return shells
}

func eachPowerShell(t *testing.T, test func(t *testing.T, shell string)) {
	t.Helper()
	for _, shell := range powerShells(t) {
		t.Run(strings.TrimSuffix(filepath.Base(shell), ".exe"), func(t *testing.T) {
			test(t, shell)
		})
	}
}

func inheritedOnWindows(name string) bool {
	upper := strings.ToUpper(name)
	for _, prefix := range []string{"ZENSU_", "GITHUB_", "RUNNER_", "ACTIONS_"} {
		if strings.HasPrefix(upper, prefix) {
			return false
		}
	}
	return upper != "PSMODULEPATH" && upper != "TEMP" && upper != "TMP" && upper != "CI" && upper != ""
}

func powerShellEnv(t *testing.T, vars map[string]string) []string {
	t.Helper()
	temp := filepath.Join(t.TempDir(), "temp dir")
	if err := os.MkdirAll(temp, 0o755); err != nil {
		t.Fatal(err)
	}
	var env []string
	if runtime.GOOS == "windows" {
		for _, kv := range os.Environ() {
			if name, _, _ := strings.Cut(kv, "="); inheritedOnWindows(name) {
				env = append(env, kv)
			}
		}
		env = append(env, "TEMP="+temp, "TMP="+temp)
	} else {
		env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "TMPDIR=" + temp, "LANG=C", "LC_ALL=C"}
	}
	for k, v := range vars {
		env = append(env, k+"="+v)
	}
	return env
}

func runInstallPS1(t *testing.T, shell, script string, env map[string]string) installRun {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	vars := map[string]string{"ZENSU_INSTALL_DIR": bin, "ZENSU_NO_MODIFY_PATH": "1"}
	for k, v := range env {
		vars[k] = v
	}
	cmd := exec.Command(shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script)
	cmd.Env = powerShellEnv(t, vars)
	out, err := cmd.CombinedOutput()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("running install.ps1: %v", err)
	}
	return installRun{output: strings.ReplaceAll(string(out), "\r\n", "\n"), exitCode: code, bin: filepath.Join(bin, "zensu.exe")}
}

func patchedInstallPS1(t *testing.T, dir string, keys ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !windowsReleaseKeysLine.Match(data) {
		t.Fatal("install.ps1 has no Get-ReleaseKeys function with one key list")
	}
	quoted := make([]string, len(keys))
	for i, key := range keys {
		quoted[i] = "'" + key + "'"
	}
	patched := windowsReleaseKeysLine.ReplaceAllLiteral(data, []byte("function Get-ReleaseKeys {\n    @("+strings.Join(quoted, ", ")+")\n}"))
	path := filepath.Join(dir, "install.ps1")
	if err := os.WriteFile(path, patched, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type signingKey struct {
	public  string
	private ed25519.PrivateKey
}

func newSigningKey(t *testing.T) signingKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signingKey{public: base64.StdEncoding.EncodeToString(pub), private: priv}
}

func (k signingKey) sign(message []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(k.private, message))
}

func patchedInstallScript(t *testing.T, dir string, keys ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !releaseKeysLine.Match(data) {
		t.Fatal("install.sh has no release_keys function with one echo line")
	}
	patched := releaseKeysLine.ReplaceAllLiteral(data, []byte("release_keys() {\n\techo \""+strings.Join(keys, " ")+"\"\n}"))
	path := filepath.Join(dir, "install.sh")
	if err := os.WriteFile(path, patched, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeRelease struct {
	t         *testing.T
	mu        sync.Mutex
	requests  int
	paths     []string
	tag       string
	root      string
	server    *httptest.Server
	archive   string
	checksums string
}

func archiveName(ver string) string {
	return fmt.Sprintf("zensu_%s_%s_%s.tar.gz", ver, runtime.GOOS, runtime.GOARCH)
}

func windowsArchiveName(ver string) string {
	return "zensu_" + ver + "_windows_amd64.zip"
}

func newFakeRelease(t *testing.T, tag string, binary []byte, key signingKey) *fakeRelease {
	t.Helper()
	return newFakeReleaseOf(t, tag, tarball(t, binary), key)
}

func newWindowsRelease(t *testing.T, tag string, binary []byte, key signingKey) *fakeRelease {
	t.Helper()
	return newFakeReleaseNamed(t, tag, windowsArchiveName(strings.TrimPrefix(tag, "v")), zipball(t, binary), key)
}

func newFakeReleaseOf(t *testing.T, tag string, archive []byte, key signingKey) *fakeRelease {
	t.Helper()
	return newFakeReleaseNamed(t, tag, archiveName(strings.TrimPrefix(tag, "v")), archive, key)
}

func newFakeReleaseNamed(t *testing.T, tag, name string, archive []byte, key signingKey) *fakeRelease {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	r := &fakeRelease{t: t, tag: tag, root: t.TempDir(), archive: name, checksums: "zensu_" + ver + "_checksums.txt"}
	if err := os.MkdirAll(r.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	r.write(r.archive, archive)
	sum := sha256.Sum256(r.read(r.archive))
	checksums := fmt.Sprintf("%s  %s.sbom.json\n%s  %s\n", strings.Repeat("0", 64), r.archive, hex.EncodeToString(sum[:]), r.archive)
	r.write(r.checksums, []byte(checksums))
	r.write(r.checksums+".sig", []byte(key.sign([]byte(checksums))+"\n"))
	r.serve()
	return r
}

func newMirror(t *testing.T, tag, archive string, files map[string][]byte) *fakeRelease {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	r := &fakeRelease{t: t, tag: tag, root: t.TempDir(), archive: archive, checksums: "zensu_" + ver + "_checksums.txt"}
	if err := os.MkdirAll(r.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		r.write(name, data)
	}
	r.serve()
	return r
}

func (r *fakeRelease) serve() {
	files := http.FileServer(http.Dir(r.root))
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests++
		r.paths = append(r.paths, req.URL.Path)
		r.mu.Unlock()
		files.ServeHTTP(w, req)
	}))
	r.t.Cleanup(r.server.Close)
}

func (r *fakeRelease) downloads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
}

func (r *fakeRelease) requested(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, path := range r.paths {
		if path == "/"+r.tag+"/"+name {
			return true
		}
	}
	return false
}

func (r *fakeRelease) requestLog() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.paths...)
}

func (r *fakeRelease) dir() string { return filepath.Join(r.root, r.tag) }

func (r *fakeRelease) write(name string, data []byte) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir(), name), data, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fakeRelease) read(name string) []byte {
	r.t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir(), name))
	if err != nil {
		r.t.Fatal(err)
	}
	return data
}

func (r *fakeRelease) remove(name string) {
	r.t.Helper()
	if err := os.Remove(filepath.Join(r.dir(), name)); err != nil {
		r.t.Fatal(err)
	}
}

func tarball(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		mode int64
		data []byte
	}{
		{"zensu", 0o755, binary},
		{"README.md", 0o644, []byte("# Zensu CLI\n")},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipball(t *testing.T, binary []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{"zensu.exe", 0o755, binary},
		{"README.md", 0o644, []byte("# Zensu CLI\n")},
	} {
		header := &zip.FileHeader{Name: f.name, Method: zip.Deflate}
		header.SetMode(f.mode)
		w, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func runnerEnv(t *testing.T, extra map[string]string) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
		"LANG=C",
		"LC_ALL=C",
	}
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
