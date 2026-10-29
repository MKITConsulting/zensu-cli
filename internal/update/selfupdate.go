package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	maxChecksumsBytes = 64 << 10
	maxSignatureBytes = 4 << 10
	maxArchiveBytes   = 128 << 20
	downloadTimeout   = 10 * time.Minute
	smokeTestTimeout  = 30 * time.Second
)

type Result struct {
	Previous   string
	Version    string
	Executable string
}

var isRoot = func() bool { return os.Geteuid() == 0 }

var runVersion = func(ctx context.Context, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Env = append(os.Environ(), "ZENSU_NO_UPDATE_CHECK=1")
	return cmd.Output()
}

var maxBinaryBytes int64 = 256 << 20

var downloadBase = "https://github.com/" + repo + "/releases/download"

var redirectHosts = map[string]bool{
	"github.com":                           true,
	"release-assets.githubusercontent.com": true,
	"objects.githubusercontent.com":        true,
}

var downloadClient = &http.Client{CheckRedirect: checkDownloadRedirect}

var errAssetMissing = errors.New("not found in the release")

func SelfUpdate(ctx context.Context, current, tag, exe string) (Result, error) {
	if !validVersion(tag) {
		return Result{}, fmt.Errorf("invalid release tag %q", tag)
	}
	tag = normalize(tag)
	ver := strings.TrimPrefix(tag, "v")
	name, err := archiveName(ver, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".zensu-update-*"+exeSuffix())
	if err != nil {
		return Result{}, writeError(dir, err)
	}
	defer os.Remove(tmp.Name())
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	err = fetchVerifiedBinary(ctx, current, tag, name, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Result{}, err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return Result{}, err
	}
	if err := smokeTest(ctx, tmp.Name(), ver); err != nil {
		return Result{}, err
	}
	if err := replaceExecutable(tmp.Name(), exe); err != nil {
		return Result{}, writeError(dir, err)
	}
	if !isRoot() {
		_ = saveCache(cache{CheckedAt: time.Now(), LatestVersion: tag})
	}
	return Result{Previous: current, Version: ver, Executable: exe}, nil
}

func fetchVerifiedBinary(ctx context.Context, current, tag, name string, w *os.File) error {
	ver := strings.TrimPrefix(tag, "v")
	base := strings.TrimRight(downloadBase, "/") + "/" + tag + "/"
	checksums := checksumsName(ver)
	sums, err := fetchAsset(ctx, base+checksums, maxChecksumsBytes, current)
	if err != nil {
		return err
	}
	sigs, err := fetchAsset(ctx, base+checksums+".sig", maxSignatureBytes, current)
	if errors.Is(err, errAssetMissing) {
		return fmt.Errorf("release %s carries no signature, so zensu update cannot verify it and changed nothing; please report this at https://github.com/MKITConsulting/zensu-cli/issues", tag)
	}
	if err != nil {
		return err
	}
	if err := VerifySignature(sums, sigs, releaseKeys); err != nil {
		return fmt.Errorf("release %s: %w", tag, err)
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return err
	}
	archive, err := fetchAsset(ctx, base+name, maxArchiveBytes, current)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); !bytes.Equal(got[:], want) {
		return fmt.Errorf("checksum mismatch for %s", name)
	}
	if err := extractBinary(archive, strings.HasSuffix(name, ".zip"), "zensu"+exeSuffix(), w); err != nil {
		return err
	}
	return w.Sync()
}

func smokeTest(ctx context.Context, path, ver string) error {
	line, err := versionLine(ctx, path)
	if err != nil {
		return fmt.Errorf("the downloaded zensu %s does not run on this machine: %w", ver, err)
	}
	if !reportsVersion(line, ver) {
		return fmt.Errorf("the downloaded binary reports %q instead of zensu %s", clip(line), ver)
	}
	return nil
}

func VerifyInstalled(ctx context.Context, path, ver string) error {
	line, err := versionLine(ctx, path)
	if err != nil {
		return fmt.Errorf("%s does not run: %w", path, err)
	}
	if !reportsVersion(line, ver) {
		return fmt.Errorf("%s reports %q instead of zensu %s", path, clip(line), ver)
	}
	return nil
}

func versionLine(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, smokeTestTimeout)
	defer cancel()
	out, err := runVersion(ctx, path)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line), nil
}

func reportsVersion(line, ver string) bool {
	want := "zensu " + ver
	return line == want || strings.HasPrefix(line, want+" ")
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80]
	}
	return s
}

func checksumsName(ver string) string {
	return "zensu_" + ver + "_checksums.txt"
}

var renameBackoff = []time.Duration{50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}

func replaceExecutable(src, dst string) error {
	if runtime.GOOS == "windows" {
		return replaceViaBackup(retryRename(os.Rename, time.Sleep), os.Remove, src, dst)
	}
	return os.Rename(src, dst)
}

func retryRename(rename func(string, string) error, sleep func(time.Duration)) func(string, string) error {
	return func(src, dst string) error {
		err := rename(src, dst)
		for _, wait := range renameBackoff {
			if err == nil {
				return nil
			}
			sleep(wait)
			err = rename(src, dst)
		}
		return err
	}
}

func replaceViaBackup(rename func(string, string) error, remove func(string) error, src, dst string) error {
	old := dst + ".old"
	_ = remove(old)
	if err := rename(dst, old); err != nil {
		return err
	}
	if err := rename(src, dst); err != nil {
		if restoreErr := rename(old, dst); restoreErr != nil {
			return fmt.Errorf("%w; restoring %s from %s also failed: %v", err, dst, old, restoreErr)
		}
		return err
	}
	_ = remove(old)
	return nil
}

func writeError(dir string, err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot write to %s: %w; %s", dir, err, permissionHint())
	}
	return err
}

func permissionHint() string {
	if runtime.GOOS == "windows" {
		return "re-run zensu update from an elevated (administrator) terminal"
	}
	return "re-run with: sudo zensu update"
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func checkDownloadRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" || !redirectHosts[req.URL.Hostname()] {
		return fmt.Errorf("refusing redirect to %s://%s", req.URL.Scheme, req.URL.Host)
	}
	return nil
}

func fetchAsset(ctx context.Context, url string, limit int64, current string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	name := path.Base(req.URL.Path)
	req.Header.Set("User-Agent", "zensu-cli/"+current)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%s: %w", name, errAssetMissing)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("downloading %s: status %d", name, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("downloading %s: larger than %d bytes", name, limit)
	}
	return data, nil
}

func ReleaseKeys() []string {
	return append([]string(nil), releaseKeys...)
}

func VerifySignature(message, signatures []byte, keys []string) error {
	for _, line := range strings.Fields(string(signatures)) {
		sig, err := base64.StdEncoding.DecodeString(line)
		if err != nil || len(sig) != ed25519.SignatureSize {
			continue
		}
		for _, k := range keys {
			pub, err := base64.StdEncoding.DecodeString(k)
			if err == nil && len(pub) == ed25519.PublicKeySize && ed25519.Verify(pub, message, sig) {
				return nil
			}
		}
	}
	return errors.New("the release signature does not match any trusted zensu release key")
}

func checksumFor(checksums []byte, name string) ([]byte, error) {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}
		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return nil, fmt.Errorf("malformed checksum entry for %s", name)
		}
		return sum, nil
	}
	return nil, fmt.Errorf("the release checksums list no entry for %s", name)
}

func archiveName(ver, goos, goarch string) (string, error) {
	if goos == "windows" && goarch == "arm64" {
		goarch = "amd64"
	}
	if (goos != "linux" && goos != "darwin" && goos != "windows") || (goarch != "amd64" && goarch != "arm64") {
		return "", fmt.Errorf("no zensu release is built for %s/%s", goos, goarch)
	}
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("zensu_%s_%s_%s%s", ver, goos, goarch, ext), nil
}

func extractBinary(archive []byte, zipped bool, name string, w io.Writer) error {
	if zipped {
		return extractFromZip(archive, name, w)
	}
	return extractFromTarGz(archive, name, w)
}

func extractFromTarGz(archive []byte, name string, w io.Writer) error {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("reading the release archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("the release archive contains no %s", name)
		}
		if err != nil {
			return fmt.Errorf("reading the release archive: %w", err)
		}
		if path.Clean(hdr.Name) != name {
			continue
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("%s in the release archive is not a regular file", name)
		}
		return copyBinary(w, tr, hdr.Size)
	}
}

func extractFromZip(archive []byte, name string, w io.Writer) error {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("reading the release archive: %w", err)
	}
	for _, f := range zr.File {
		if path.Clean(f.Name) != name {
			continue
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("%s in the release archive is not a regular file", name)
		}
		if f.UncompressedSize64 > uint64(maxBinaryBytes) {
			return fmt.Errorf("the release binary is larger than %d bytes", maxBinaryBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("reading the release archive: %w", err)
		}
		defer rc.Close()
		return copyBinary(w, rc, int64(f.UncompressedSize64))
	}
	return fmt.Errorf("the release archive contains no %s", name)
}

func copyBinary(w io.Writer, r io.Reader, size int64) error {
	if size > maxBinaryBytes {
		return fmt.Errorf("the release binary is larger than %d bytes", maxBinaryBytes)
	}
	n, err := io.Copy(w, io.LimitReader(r, maxBinaryBytes+1))
	if err != nil {
		return fmt.Errorf("extracting the release binary: %w", err)
	}
	if n > maxBinaryBytes {
		return fmt.Errorf("the release binary is larger than %d bytes", maxBinaryBytes)
	}
	return nil
}
