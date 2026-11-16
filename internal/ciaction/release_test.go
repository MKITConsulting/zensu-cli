package ciaction

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

var releaseKeysLine = regexp.MustCompile(`(?m)^release_keys\(\) \{\n\techo "([^"]*)"\n\}$`)

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

func requireOpenSSL3(t *testing.T) {
	t.Helper()
	for _, candidate := range []string{"openssl", "/opt/homebrew/opt/openssl@3/bin/openssl", "/usr/local/opt/openssl@3/bin/openssl"} {
		out, err := exec.Command(candidate, "version").Output()
		if err == nil && regexp.MustCompile(`^OpenSSL ([3-9]\.|[1-9][0-9])`).Match(out) {
			return
		}
	}
	if os.Getenv("CI") != "" {
		t.Fatal("verifying release signatures needs OpenSSL 3 or later on the CI runner")
	}
	t.Skip("no OpenSSL 3 or later on this machine")
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
	tag       string
	root      string
	server    *httptest.Server
	archive   string
	checksums string
}

func archiveName(ver string) string {
	return fmt.Sprintf("zensu_%s_%s_%s.tar.gz", ver, runtime.GOOS, runtime.GOARCH)
}

func newFakeRelease(t *testing.T, tag string, binary []byte, key signingKey) *fakeRelease {
	t.Helper()
	return newFakeReleaseOf(t, tag, tarball(t, binary), key)
}

func newFakeReleaseOf(t *testing.T, tag string, archive []byte, key signingKey) *fakeRelease {
	t.Helper()
	ver := strings.TrimPrefix(tag, "v")
	r := &fakeRelease{t: t, tag: tag, root: t.TempDir(), archive: archiveName(ver), checksums: "zensu_" + ver + "_checksums.txt"}
	if err := os.MkdirAll(r.dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	r.write(r.archive, archive)
	sum := sha256.Sum256(r.read(r.archive))
	checksums := fmt.Sprintf("%s  %s.sbom.json\n%s  %s\n", strings.Repeat("0", 64), r.archive, hex.EncodeToString(sum[:]), r.archive)
	r.write(r.checksums, []byte(checksums))
	r.write(r.checksums+".sig", []byte(key.sign([]byte(checksums))+"\n"))
	files := http.FileServer(http.Dir(r.root))
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests++
		r.mu.Unlock()
		files.ServeHTTP(w, req)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *fakeRelease) downloads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.requests
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
