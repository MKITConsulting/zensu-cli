package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type archiveEntry struct {
	name     string
	body     []byte
	typeflag byte
	linkname string
}

func makeTarGz(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typeflag := e.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: typeflag, Linkname: e.linkname}
		if typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typeflag == tar.TypeReg {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
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

func makeZip(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

func signLine(priv ed25519.PrivateKey, msg []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, msg))
}

func TestVerifySignature(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, otherPriv := newKey(t)
	msg := []byte("abc  zensu_1.3.0_linux_amd64.tar.gz\n")
	tests := []struct {
		name    string
		msg     []byte
		sigs    string
		keys    []string
		wantErr bool
	}{
		{"valid", msg, signLine(priv, msg) + "\n", []string{pub}, false},
		{"tampered message", append([]byte("x"), msg...), signLine(priv, msg), []string{pub}, true},
		{"wrong key", msg, signLine(otherPriv, msg), []string{pub}, true},
		{"rotation second key", msg, signLine(priv, msg), []string{otherPub, pub}, false},
		{"one of several signatures", msg, "not-base64!\n" + signLine(otherPriv, msg) + "\n" + signLine(priv, msg) + "\n", []string{pub}, false},
		{"garbage signature", msg, "bm90IGEgc2lnbmF0dXJl", []string{pub}, true},
		{"empty signature", msg, "", []string{pub}, true},
		{"no trusted keys", msg, signLine(priv, msg), nil, true},
		{"malformed key ignored", msg, signLine(priv, msg), []string{"%%%", pub}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifySignature(tt.msg, []byte(tt.sigs), tt.keys)
			if (err != nil) != tt.wantErr {
				t.Errorf("VerifySignature error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestReleaseKeysReturnsCopy(t *testing.T) {
	old := releaseKeys
	releaseKeys = []string{"a"}
	t.Cleanup(func() { releaseKeys = old })
	keys := ReleaseKeys()
	keys[0] = "mutated"
	if releaseKeys[0] != "a" {
		t.Error("ReleaseKeys must not expose the embedded key slice")
	}
}

func TestChecksumFor(t *testing.T) {
	sum := sha256.Sum256([]byte("archive"))
	hexSum := fmt.Sprintf("%x", sum)
	sums := []byte(hexSum + "  zensu_1.3.0_darwin_arm64.tar.gz\n" + strings.Repeat("0", 64) + "  zensu_1.3.0_linux_amd64.tar.gz\n")

	got, err := checksumFor(sums, "zensu_1.3.0_darwin_arm64.tar.gz")
	if err != nil || !bytes.Equal(got, sum[:]) {
		t.Errorf("checksumFor = %x, %v; want %s", got, err, hexSum)
	}
	if got, err := checksumFor([]byte(hexSum+" *zensu.zip\n"), "zensu.zip"); err != nil || !bytes.Equal(got, sum[:]) {
		t.Errorf("binary-mode entry: checksumFor = %x, %v", got, err)
	}
	if _, err := checksumFor(sums, "zensu_1.3.0_windows_amd64.zip"); err == nil || !strings.Contains(err.Error(), "no entry") {
		t.Errorf("missing entry error = %v", err)
	}
	if _, err := checksumFor([]byte("zz  a.tar.gz\n"), "a.tar.gz"); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("non-hex entry error = %v", err)
	}
	if _, err := checksumFor([]byte("abcd  a.tar.gz\n"), "a.tar.gz"); err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Errorf("short entry error = %v", err)
	}
	if _, err := checksumFor([]byte(hexSum+"  sub/zensu_1.3.0_darwin_arm64.tar.gz\n"), "zensu_1.3.0_darwin_arm64.tar.gz"); err == nil {
		t.Error("an entry for a different path must not match")
	}
}

func TestArchiveName(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
		wantErr      bool
	}{
		{"darwin", "arm64", "zensu_1.3.0_darwin_arm64.tar.gz", false},
		{"darwin", "amd64", "zensu_1.3.0_darwin_amd64.tar.gz", false},
		{"linux", "amd64", "zensu_1.3.0_linux_amd64.tar.gz", false},
		{"linux", "arm64", "zensu_1.3.0_linux_arm64.tar.gz", false},
		{"windows", "amd64", "zensu_1.3.0_windows_amd64.zip", false},
		{"windows", "arm64", "zensu_1.3.0_windows_amd64.zip", false},
		{"freebsd", "amd64", "", true},
		{"linux", "386", "", true},
	}
	for _, tt := range tests {
		got, err := archiveName("1.3.0", tt.goos, tt.goarch)
		if got != tt.want || (err != nil) != tt.wantErr {
			t.Errorf("archiveName(%s/%s) = %q, %v; want %q, wantErr %v", tt.goos, tt.goarch, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestExtractBinary(t *testing.T) {
	bin := []byte("#!/bin/sh\necho zensu\n")
	readme := []byte("readme")
	tests := []struct {
		name    string
		archive []byte
		zipped  bool
		entry   string
		wantErr string
	}{
		{"tar.gz", makeTarGz(t, archiveEntry{name: "README.md", body: readme}, archiveEntry{name: "zensu", body: bin}), false, "zensu", ""},
		{"tar.gz dot prefix", makeTarGz(t, archiveEntry{name: "./zensu", body: bin}), false, "zensu", ""},
		{"zip", makeZip(t, archiveEntry{name: "LICENSE", body: readme}, archiveEntry{name: "zensu.exe", body: bin}), true, "zensu.exe", ""},
		{"tar.gz missing", makeTarGz(t, archiveEntry{name: "README.md", body: readme}), false, "zensu", "contains no zensu"},
		{"zip missing", makeZip(t, archiveEntry{name: "zensu", body: bin}), true, "zensu.exe", "contains no zensu.exe"},
		{"tar.gz nested only", makeTarGz(t, archiveEntry{name: "dir/zensu", body: bin}), false, "zensu", "contains no zensu"},
		{"tar.gz symlink", makeTarGz(t, archiveEntry{name: "zensu", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"}), false, "zensu", "not a regular file"},
		{"not gzip", []byte("plain"), false, "zensu", "reading the release archive"},
		{"not zip", []byte("plain"), true, "zensu.exe", "reading the release archive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := extractBinary(tt.archive, tt.zipped, tt.entry, &out)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("extractBinary error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), bin) {
				t.Errorf("extracted %q, want %q", out.Bytes(), bin)
			}
		})
	}
}

func TestExtractBinaryRejectsOversizeEntry(t *testing.T) {
	old := maxBinaryBytes
	maxBinaryBytes = 4
	t.Cleanup(func() { maxBinaryBytes = old })
	big := []byte("0123456789")
	for name, archive := range map[string][]byte{
		"tar.gz": makeTarGz(t, archiveEntry{name: "zensu", body: big}),
		"zip":    makeZip(t, archiveEntry{name: "zensu", body: big}),
	} {
		var out bytes.Buffer
		err := extractBinary(archive, name == "zip", "zensu", &out)
		if err == nil || !strings.Contains(err.Error(), "larger than") {
			t.Errorf("%s: error = %v, want a size refusal", name, err)
		}
	}
}

func TestCheckDownloadRedirect(t *testing.T) {
	mk := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: u}
	}
	via := []*http.Request{mk("https://github.com/x")}
	tests := []struct {
		target  string
		via     []*http.Request
		wantErr bool
	}{
		{"https://release-assets.githubusercontent.com/github-production-release-asset/1?sig=x", via, false},
		{"https://objects.githubusercontent.com/a", via, false},
		{"https://github.com/MKITConsulting/zensu-cli/releases/download/v1/a", via, false},
		{"http://release-assets.githubusercontent.com/a", via, true},
		{"https://evil.example/a", via, true},
		{"https://release-assets.githubusercontent.com.evil.example/a", via, true},
		{"https://github.com/a", make([]*http.Request, 10), true},
	}
	for _, tt := range tests {
		if err := checkDownloadRedirect(mk(tt.target), tt.via); (err != nil) != tt.wantErr {
			t.Errorf("checkDownloadRedirect(%s) = %v, wantErr %v", tt.target, err, tt.wantErr)
		}
	}
}

func serveAssets(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = checkDownloadRedirect
	oldBase, oldClient := downloadBase, downloadClient
	downloadBase, downloadClient = srv.URL, client
	t.Cleanup(func() { downloadBase, downloadClient = oldBase, oldClient })
	return srv
}

func TestFetchAsset(t *testing.T) {
	srv := serveAssets(t, func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua != "zensu-cli/1.2.3" {
			t.Errorf("User-Agent = %q", ua)
		}
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("payload"))
		case "/big":
			_, _ = w.Write([]byte(strings.Repeat("x", 11)))
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
		case "/elsewhere":
			http.Redirect(w, r, "https://evil.example/payload", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()

	if got, err := fetchAsset(ctx, srv.URL+"/ok", 10, "1.2.3"); err != nil || string(got) != "payload" {
		t.Errorf("fetchAsset ok = %q, %v", got, err)
	}
	if _, err := fetchAsset(ctx, srv.URL+"/big", 10, "1.2.3"); err == nil || !strings.Contains(err.Error(), "larger than 10 bytes") {
		t.Errorf("oversize error = %v", err)
	}
	if _, err := fetchAsset(ctx, srv.URL+"/missing.sig", 10, "1.2.3"); !errors.Is(err, errAssetMissing) {
		t.Errorf("404 error = %v, want errAssetMissing", err)
	}
	if _, err := fetchAsset(ctx, srv.URL+"/boom", 10, "1.2.3"); err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Errorf("500 error = %v", err)
	}
	if _, err := fetchAsset(ctx, srv.URL+"/elsewhere", 10, "1.2.3"); err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Errorf("foreign redirect error = %v", err)
	}
}

func fakeBinary(ver string) []byte {
	return []byte("#!/bin/sh\necho \"zensu " + ver + " (commit test, built test)\"\n")
}

func releaseFiles(t *testing.T, ver string, bin []byte, priv ed25519.PrivateKey) map[string][]byte {
	t.Helper()
	name, err := archiveName(ver, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Skip(err)
	}
	entry := archiveEntry{name: "zensu" + exeSuffix(), body: bin}
	archive := makeTarGz(t, archiveEntry{name: "README.md", body: []byte("readme")}, entry)
	if strings.HasSuffix(name, ".zip") {
		archive = makeZip(t, entry)
	}
	sum := sha256.Sum256(archive)
	sums := []byte(fmt.Sprintf("%x  %s\n%s  zensu_%s_plan9_amd64.tar.gz\n", sum, name, strings.Repeat("0", 64), ver))
	prefix := "v" + ver + "/"
	return map[string][]byte{
		prefix + "zensu_" + ver + "_checksums.txt":     sums,
		prefix + "zensu_" + ver + "_checksums.txt.sig": []byte(signLine(priv, sums) + "\n"),
		prefix + name: archive,
	}
}

func serveRelease(t *testing.T, files map[string][]byte) *uint32 {
	t.Helper()
	var hits uint32
	serveAssets(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddUint32(&hits, 1)
		data, ok := files[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	return &hits
}

func installedBinary(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake release binary is a shell script")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "zensu")
	if err := os.WriteFile(exe, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, exe
}

func trustKey(t *testing.T, keys ...string) {
	t.Helper()
	oldKeys, oldRoot := releaseKeys, isRoot
	releaseKeys = keys
	isRoot = func() bool { return false }
	t.Cleanup(func() { releaseKeys, isRoot = oldKeys, oldRoot })
}

func assertUntouched(t *testing.T, dir, exe string) {
	t.Helper()
	data, err := os.ReadFile(exe)
	if err != nil || string(data) != "old binary" {
		t.Errorf("installed binary changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "zensu" {
			t.Errorf("leftover file in the install dir: %s", e.Name())
		}
	}
}

func TestSelfUpdateReplacesBinary(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	pub, priv := newKey(t)
	trustKey(t, pub)
	serveRelease(t, releaseFiles(t, "1.3.0", fakeBinary("1.3.0"), priv))
	dir, exe := installedBinary(t)

	res, err := SelfUpdate(context.Background(), "1.2.3", "v1.3.0", exe)
	if err != nil {
		t.Fatalf("SelfUpdate: %v", err)
	}
	if want := (Result{Previous: "1.2.3", Version: "1.3.0", Executable: exe}); res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}
	data, err := os.ReadFile(exe)
	if err != nil || !bytes.Equal(data, fakeBinary("1.3.0")) {
		t.Errorf("installed binary = %q, %v; want the release binary", data, err)
	}
	info, err := os.Stat(exe)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("installed binary mode = %v, %v; want 0755", info, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("install dir holds %d entries, want only zensu", len(entries))
	}
	if c, err := loadCache(); err != nil || c.LatestVersion != "v1.3.0" {
		t.Errorf("version cache = %q, %v; want v1.3.0", c.LatestVersion, err)
	}
}

func TestSelfUpdateRefusesUnverifiedReleases(t *testing.T) {
	pub, priv := newKey(t)
	otherPub, _ := newKey(t)
	sumsKey := "v1.3.0/zensu_1.3.0_checksums.txt"
	corruptArchive := func(f map[string][]byte) {
		for k := range f {
			if strings.HasSuffix(k, ".tar.gz") || strings.HasSuffix(k, ".zip") {
				f[k] = append(f[k], 0)
			}
		}
	}
	tests := []struct {
		name    string
		bin     []byte
		keys    []string
		mutate  func(map[string][]byte)
		wantErr string
	}{
		{"unsigned release", fakeBinary("1.3.0"), []string{pub}, func(f map[string][]byte) { delete(f, sumsKey+".sig") }, "carries no signature, so zensu update cannot verify it and changed nothing"},
		{"untrusted key", fakeBinary("1.3.0"), []string{otherPub}, nil, "does not match any trusted zensu release key"},
		{"tampered checksums", fakeBinary("1.3.0"), []string{pub}, func(f map[string][]byte) { f[sumsKey] = append(f[sumsKey], []byte("00  extra\n")...) }, "does not match any trusted zensu release key"},
		{"missing checksums", fakeBinary("1.3.0"), []string{pub}, func(f map[string][]byte) { delete(f, sumsKey) }, "not found in the release"},
		{"archive does not match checksum", fakeBinary("1.3.0"), []string{pub}, corruptArchive, "checksum mismatch"},
		{"release lacks this platform", fakeBinary("1.3.0"), []string{pub}, func(f map[string][]byte) {
			sums := []byte(strings.Repeat("0", 64) + "  zensu_1.3.0_plan9_amd64.tar.gz\n")
			f[sumsKey], f[sumsKey+".sig"] = sums, []byte(signLine(priv, sums)+"\n")
		}, "the release checksums list no entry for zensu_1.3.0_"},
		{"archive missing from the release", fakeBinary("1.3.0"), []string{pub}, func(f map[string][]byte) {
			for k := range f {
				if strings.HasSuffix(k, ".tar.gz") || strings.HasSuffix(k, ".zip") {
					delete(f, k)
				}
			}
		}, "not found in the release"},
		{"binary reports another version", fakeBinary("9.9.9"), []string{pub}, nil, "instead of zensu 1.3.0"},
		{"binary does not start", []byte("#!/bin/sh\nexit 3\n"), []string{pub}, nil, "does not run on this machine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
			trustKey(t, tt.keys...)
			files := releaseFiles(t, "1.3.0", tt.bin, priv)
			if tt.mutate != nil {
				tt.mutate(files)
			}
			serveRelease(t, files)
			dir, exe := installedBinary(t)

			_, err := SelfUpdate(context.Background(), "1.2.3", "v1.3.0", exe)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("SelfUpdate error = %v, want %q", err, tt.wantErr)
			}
			assertUntouched(t, dir, exe)
			if _, err := loadCache(); err == nil {
				t.Error("a refused update must not touch the version cache")
			}
		})
	}
}

func TestSelfUpdateUnwritableInstallDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	pub, priv := newKey(t)
	trustKey(t, pub)
	hits := serveRelease(t, releaseFiles(t, "1.3.0", fakeBinary("1.3.0"), priv))
	dir, exe := installedBinary(t)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	_, err := SelfUpdate(context.Background(), "1.2.3", "v1.3.0", exe)
	if err == nil || !strings.Contains(err.Error(), "permission denied") || !strings.Contains(err.Error(), "sudo zensu update") {
		t.Fatalf("SelfUpdate error = %v, want a permission error naming sudo zensu update", err)
	}
	if n := atomic.LoadUint32(hits); n != 0 {
		t.Errorf("an unwritable install dir must fail before downloading, got %d requests", n)
	}
	assertUntouched(t, dir, exe)
}

func TestSelfUpdateAsRootSkipsVersionCache(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	pub, priv := newKey(t)
	trustKey(t, pub)
	isRoot = func() bool { return true }
	serveRelease(t, releaseFiles(t, "1.3.0", fakeBinary("1.3.0"), priv))
	_, exe := installedBinary(t)

	if _, err := SelfUpdate(context.Background(), "1.2.3", "v1.3.0", exe); err != nil {
		t.Fatalf("SelfUpdate: %v", err)
	}
	if _, err := loadCache(); err == nil {
		t.Error("an update run as root must not create a root-owned version cache")
	}
}

func TestSelfUpdateRejectsInvalidTag(t *testing.T) {
	hits := serveRelease(t, map[string][]byte{})
	_, exe := installedBinary(t)
	for _, tag := range []string{"latest", "v1.3.0/../../evil", "v1.3.0 | sh", ""} {
		if _, err := SelfUpdate(context.Background(), "1.2.3", tag, exe); err == nil || !strings.Contains(err.Error(), "invalid release tag") {
			t.Errorf("SelfUpdate(%q) error = %v, want an invalid tag refusal", tag, err)
		}
	}
	if n := atomic.LoadUint32(hits); n != 0 {
		t.Errorf("an invalid tag must not reach the network, got %d requests", n)
	}
}

func TestSmokeTest(t *testing.T) {
	old := runVersion
	t.Cleanup(func() { runVersion = old })
	tests := []struct {
		name    string
		out     string
		err     error
		wantErr string
	}{
		{"matching version", "zensu 1.3.0 (commit abc, built now)\n", nil, ""},
		{"bare version line", "zensu 1.3.0\r\n", nil, ""},
		{"prerelease is not the release", "zensu 1.3.0-rc1 (commit abc)\n", nil, "instead of zensu 1.3.0"},
		{"other version", "zensu 1.2.9 (commit abc)\n", nil, "instead of zensu 1.3.0"},
		{"longer version", "zensu 1.3.01 (commit abc)\n", nil, "instead of zensu 1.3.0"},
		{"does not start", "", errors.New("exec format error"), "does not run on this machine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runVersion = func(context.Context, string) ([]byte, error) { return []byte(tt.out), tt.err }
			err := smokeTest(context.Background(), "/tmp/zensu", "1.3.0")
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("smokeTest = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("smokeTest = %v, want %q", err, tt.wantErr)
			}
		})
	}
	runVersion = func(context.Context, string) ([]byte, error) {
		return []byte("\x1b[2J" + strings.Repeat("x", 200)), nil
	}
	err := smokeTest(context.Background(), "/tmp/zensu", "1.3.0")
	if err == nil || strings.ContainsRune(err.Error(), 0x1b) || len(err.Error()) > 200 {
		t.Errorf("smokeTest must quote and truncate untrusted output, got %q", err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Errorf("%s = %q, %v; want %q", filepath.Base(path), data, err, want)
	}
}

func assertMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s still exists (err %v)", filepath.Base(path), err)
	}
}

func TestReplaceViaBackup(t *testing.T) {
	setup := func(t *testing.T) (string, string) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "zensu.exe")
		src := filepath.Join(dir, ".zensu-update-1.exe")
		if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
			t.Fatal(err)
		}
		return src, dst
	}
	t.Run("swaps and removes the backup", func(t *testing.T) {
		src, dst := setup(t)
		if err := os.WriteFile(dst+".old", []byte("stale"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := replaceViaBackup(os.Rename, os.Remove, src, dst); err != nil {
			t.Fatal(err)
		}
		assertFile(t, dst, "new")
		assertMissing(t, dst+".old")
		assertMissing(t, src)
	})
	t.Run("keeps a backup that is still in use", func(t *testing.T) {
		src, dst := setup(t)
		inUse := func(p string) error {
			if strings.HasSuffix(p, ".old") {
				return errors.New("in use")
			}
			return os.Remove(p)
		}
		if err := replaceViaBackup(os.Rename, inUse, src, dst); err != nil {
			t.Fatal(err)
		}
		assertFile(t, dst, "new")
		assertFile(t, dst+".old", "old")
	})
	t.Run("restores the old binary when the swap fails", func(t *testing.T) {
		src, dst := setup(t)
		failing := func(from, to string) error {
			if from == src {
				return errors.New("swap failed")
			}
			return os.Rename(from, to)
		}
		err := replaceViaBackup(failing, os.Remove, src, dst)
		if err == nil || !strings.Contains(err.Error(), "swap failed") {
			t.Fatalf("error = %v, want the swap failure", err)
		}
		assertFile(t, dst, "old")
		assertMissing(t, dst+".old")
		assertFile(t, src, "new")
	})
	t.Run("reports a failed restore", func(t *testing.T) {
		src, dst := setup(t)
		failing := func(from, to string) error {
			if from == src || from == dst+".old" {
				return errors.New("locked")
			}
			return os.Rename(from, to)
		}
		err := replaceViaBackup(failing, os.Remove, src, dst)
		if err == nil || !strings.Contains(err.Error(), "restoring") {
			t.Fatalf("error = %v, want the failed restore named", err)
		}
	})
	t.Run("leaves everything when the backup rename fails", func(t *testing.T) {
		src, dst := setup(t)
		failing := func(from, to string) error {
			if from == dst {
				return errors.New("denied")
			}
			return os.Rename(from, to)
		}
		if err := replaceViaBackup(failing, os.Remove, src, dst); err == nil {
			t.Fatal("want an error when the running binary cannot be moved aside")
		}
		assertFile(t, dst, "old")
		assertFile(t, src, "new")
	})
}

func TestWriteError(t *testing.T) {
	denied := &os.PathError{Op: "open", Path: "/usr/local/bin/x", Err: os.ErrPermission}
	perm := writeError("/usr/local/bin", denied)
	if !strings.Contains(perm.Error(), "cannot write to /usr/local/bin: open /usr/local/bin/x: permission denied") || !strings.Contains(perm.Error(), permissionHint()) {
		t.Errorf("permission error = %q", perm)
	}
	if !errors.Is(perm, os.ErrPermission) {
		t.Errorf("permission error %q must keep its cause", perm)
	}
	restore := writeError("/usr/local/bin", fmt.Errorf("%w; restoring /usr/local/bin/zensu from /usr/local/bin/zensu.old also failed: locked", denied))
	if !strings.Contains(restore.Error(), "restoring /usr/local/bin/zensu from /usr/local/bin/zensu.old also failed: locked") {
		t.Errorf("a failed restore must stay visible next to the permission hint: %q", restore)
	}
	other := errors.New("disk full")
	if got := writeError("/usr/local/bin", other); got != other {
		t.Errorf("non-permission error = %v, want it unchanged", got)
	}
}

func TestRetryRename(t *testing.T) {
	var slept []time.Duration
	sleep := func(d time.Duration) { slept = append(slept, d) }
	calls := 0
	flaky := func(string, string) error {
		calls++
		if calls < 3 {
			return errors.New("being used by another process")
		}
		return nil
	}
	if err := retryRename(flaky, sleep)("a", "b"); err != nil {
		t.Fatalf("retryRename = %v, want success after transient failures", err)
	}
	if calls != 3 || len(slept) != 2 {
		t.Errorf("calls = %d, sleeps = %v; want 3 calls and 2 sleeps", calls, slept)
	}

	calls, slept = 0, nil
	locked := errors.New("locked")
	stuck := func(string, string) error { calls++; return locked }
	if err := retryRename(stuck, sleep)("a", "b"); !errors.Is(err, locked) {
		t.Fatalf("retryRename = %v, want the last error", err)
	}
	if calls != len(renameBackoff)+1 || len(slept) != len(renameBackoff) {
		t.Errorf("calls = %d, sleeps = %v; want %d attempts", calls, slept, len(renameBackoff)+1)
	}

	calls, slept = 0, nil
	if err := retryRename(func(string, string) error { calls++; return nil }, sleep)("a", "b"); err != nil || calls != 1 || len(slept) != 0 {
		t.Errorf("a rename that succeeds at once must not retry: err %v, calls %d, sleeps %v", err, calls, slept)
	}
}

func TestVerifyInstalled(t *testing.T) {
	old := runVersion
	t.Cleanup(func() { runVersion = old })
	tests := []struct {
		name    string
		out     string
		err     error
		wantErr string
	}{
		{"new version installed", "zensu 1.3.0 (commit abc, built now)\n", nil, ""},
		{"old version still installed", "zensu 1.2.3 (commit abc, built now)\n", nil, `/opt/homebrew/bin/zensu reports "zensu 1.2.3 (commit abc, built now)" instead of zensu 1.3.0`},
		{"binary missing", "", errors.New("no such file or directory"), "/opt/homebrew/bin/zensu does not run: no such file or directory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runVersion = func(_ context.Context, path string) ([]byte, error) {
				if path != "/opt/homebrew/bin/zensu" {
					t.Errorf("ran %q, want the installed binary", path)
				}
				return []byte(tt.out), tt.err
			}
			err := VerifyInstalled(context.Background(), "/opt/homebrew/bin/zensu", "1.3.0")
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("VerifyInstalled = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("VerifyInstalled = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSelfUpdateCanceledDuringDownloadLeavesNothingBehind(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	pub, _ := newKey(t)
	trustKey(t, pub)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveAssets(t, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	})
	dir, exe := installedBinary(t)

	_, err := SelfUpdate(ctx, "1.2.3", "v1.3.0", exe)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SelfUpdate error = %v, want the cancellation", err)
	}
	assertUntouched(t, dir, exe)
}

func TestUnsignedReleaseOffersNoWeakerInstallPath(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	pub, priv := newKey(t)
	trustKey(t, pub)
	files := releaseFiles(t, "1.3.0", fakeBinary("1.3.0"), priv)
	delete(files, "v1.3.0/"+checksumsName("1.3.0")+".sig")
	serveRelease(t, files)
	_, exe := installedBinary(t)

	_, err := SelfUpdate(context.Background(), "1.2.3", "v1.3.0", exe)
	if err == nil {
		t.Fatal("an unsigned release must be refused")
	}
	for _, weaker := range []string{"install.sh", "install.ps1", "curl", "irm "} {
		if strings.Contains(err.Error(), weaker) {
			t.Errorf("refusal %q points to an install path that skips the signature check (%s)", err, weaker)
		}
	}
}
