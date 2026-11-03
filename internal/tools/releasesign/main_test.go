package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func envWith(value string) func(string) string {
	return func(name string) string {
		if name == keyEnv {
			return value
		}
		return ""
	}
}

func trust(t *testing.T, pubs ...string) {
	t.Helper()
	old := trustedKeys
	trustedKeys = func() []string { return append([]string(nil), pubs...) }
	t.Cleanup(func() { trustedKeys = old })
}

func generate(t *testing.T, dir, name string) (pub, seed string) {
	t.Helper()
	path := filepath.Join(dir, name)
	var out bytes.Buffer
	if err := run([]string{"keygen", "--private-key", path}, &out, envWith("")); err != nil {
		t.Fatalf("keygen: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String()), strings.TrimSpace(string(data))
}

func TestKeygenSignVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pub, seed := generate(t, dir, "release.key")
	trust(t, pub)

	if raw, err := base64.StdEncoding.DecodeString(pub); err != nil || len(raw) != ed25519.PublicKeySize {
		t.Fatalf("keygen printed %q, want a base64 Ed25519 public key", pub)
	}
	if raw, err := base64.StdEncoding.DecodeString(seed); err != nil || len(raw) != ed25519.SeedSize {
		t.Fatalf("private key file does not hold a base64 seed")
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(dir, "release.key")); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("private key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
		}
	}

	msg := filepath.Join(dir, "zensu_1.3.0_checksums.txt")
	if err := os.WriteFile(msg, []byte("abc  zensu_1.3.0_linux_amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := msg + ".sig"
	if err := run([]string{"sign", "--in", msg, "--out", sig}, &bytes.Buffer{}, envWith(seed+"\n")); err != nil {
		t.Fatalf("sign: %v", err)
	}
	var out bytes.Buffer
	if err := run([]string{"verify", "--in", msg, "--sig", sig, "--public-key", pub}, &out, envWith("")); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.String() != "signature OK\n" {
		t.Errorf("verify printed %q", out.String())
	}

	if err := os.WriteFile(msg, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "--in", msg, "--sig", sig, "--public-key", pub}, &bytes.Buffer{}, envWith("")); err == nil {
		t.Error("verify must reject a modified file")
	}
}

func TestKeygenNeverPrintsOrOverwritesThePrivateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release.key")
	var out bytes.Buffer
	if err := run([]string{"keygen", "--private-key", path}, &out, envWith("")); err != nil {
		t.Fatal(err)
	}
	seed, _ := os.ReadFile(path)
	if strings.Contains(out.String(), strings.TrimSpace(string(seed))) {
		t.Error("keygen must not print the private key")
	}
	if err := run([]string{"keygen", "--private-key", path}, &bytes.Buffer{}, envWith("")); err == nil {
		t.Error("keygen must refuse to overwrite an existing key file")
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(again, seed) {
		t.Error("an existing key file was modified")
	}
	if err := run([]string{"keygen"}, &bytes.Buffer{}, envWith("")); err == nil || !strings.Contains(err.Error(), "--private-key is required") {
		t.Errorf("missing flag error = %v", err)
	}
}

func TestSignWithTwoKeysForRotation(t *testing.T) {
	dir := t.TempDir()
	oldPub, oldSeed := generate(t, dir, "old.key")
	newPub, newSeed := generate(t, dir, "new.key")
	trust(t, oldPub, newPub)
	msg := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(msg, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := msg + ".sig"
	if err := run([]string{"sign", "--in", msg, "--out", sig}, &bytes.Buffer{}, envWith(oldSeed+"\n"+newSeed+"\n")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(sig)
	if lines := strings.Fields(string(data)); len(lines) != 2 {
		t.Fatalf("signature file holds %d lines, want one per key", len(lines))
	}
	for _, pub := range []string{oldPub, newPub} {
		if err := run([]string{"verify", "--in", msg, "--sig", sig, "--public-key", pub}, &bytes.Buffer{}, envWith("")); err != nil {
			t.Errorf("verify with %s: %v", pub, err)
		}
	}
}

func TestSignRejectsMissingOrMalformedKeys(t *testing.T) {
	dir := t.TempDir()
	msg := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(msg, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := msg + ".sig"
	if err := run([]string{"sign", "--in", msg, "--out", sig}, &bytes.Buffer{}, envWith("")); err == nil || !strings.Contains(err.Error(), "ZENSU_RELEASE_SIGNING_KEY is not set") {
		t.Errorf("missing key error = %v", err)
	}
	secret := base64.StdEncoding.EncodeToString([]byte("too-short-secret"))
	err := run([]string{"sign", "--in", msg, "--out", sig}, &bytes.Buffer{}, envWith(secret))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("malformed key error = %v; it must fail without echoing the key", err)
	}
	if _, err := os.Stat(sig); err == nil {
		t.Error("no signature file may be written without a valid key")
	}
	if err := run([]string{"sign", "--in", msg}, &bytes.Buffer{}, envWith("")); err == nil || !strings.Contains(err.Error(), "--in and --out are required") {
		t.Errorf("missing flag error = %v", err)
	}
}

func TestVerifyDefaultsToTheKeysBuiltIntoZensu(t *testing.T) {
	dir := t.TempDir()
	_, seed := generate(t, dir, "foreign.key")
	raw, err := base64.StdEncoding.DecodeString(seed)
	if err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(msg, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := msg + ".sig"
	line := base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(raw), []byte("sums\n")))
	if err := os.WriteFile(sig, []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"verify", "--in", msg, "--sig", sig}, &bytes.Buffer{}, envWith("")); err == nil {
		t.Error("a signature from a key that zensu does not trust must fail verification")
	}
}

func TestSignRefusesKeysZensuDoesNotTrust(t *testing.T) {
	dir := t.TempDir()
	trustedPub, _ := generate(t, dir, "trusted.key")
	_, foreignSeed := generate(t, dir, "foreign.key")
	trust(t, trustedPub)
	msg := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(msg, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := msg + ".sig"
	err := run([]string{"sign", "--in", msg, "--out", sig}, &bytes.Buffer{}, envWith(foreignSeed))
	if err == nil || !strings.Contains(err.Error(), "internal/update/keys.go") || strings.Contains(err.Error(), foreignSeed) {
		t.Fatalf("sign with an untrusted key = %v; want a refusal naming keys.go without echoing the key", err)
	}
	if _, err := os.Stat(sig); err == nil {
		t.Error("no signature file may be written for a release zensu update could not verify")
	}
}

func TestSignKeepsARetiredKeyNextToATrustedOne(t *testing.T) {
	dir := t.TempDir()
	retiredPub, retiredSeed := generate(t, dir, "retired.key")
	currentPub, currentSeed := generate(t, dir, "current.key")
	trust(t, currentPub)
	msg := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(msg, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sig := msg + ".sig"
	if err := run([]string{"sign", "--in", msg, "--out", sig}, &bytes.Buffer{}, envWith(retiredSeed+"\n"+currentSeed)); err != nil {
		t.Fatalf("sign with a retired and a trusted key: %v", err)
	}
	if err := run([]string{"verify", "--in", msg, "--sig", sig, "--public-key", retiredPub}, &bytes.Buffer{}, envWith("")); err != nil {
		t.Errorf("CLIs that still trust only the retired key must verify the release: %v", err)
	}
}

func TestRunReportsFlagAndFileErrors(t *testing.T) {
	dir := t.TempDir()
	_, seed := generate(t, dir, "release.key")
	existing := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(existing, []byte("sums\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing.txt")
	tests := []struct {
		name    string
		args    []string
		env     string
		wantErr string
	}{
		{"keygen unknown flag", []string{"keygen", "--bogus"}, "", "bogus"},
		{"keygen into a missing directory", []string{"keygen", "--private-key", filepath.Join(dir, "no", "such", "dir", "k")}, "", "no such file"},
		{"sign unknown flag", []string{"sign", "--bogus"}, "", "bogus"},
		{"sign missing input", []string{"sign", "--in", missing, "--out", missing + ".sig"}, seed, "no such file"},
		{"verify unknown flag", []string{"verify", "--bogus"}, "", "bogus"},
		{"verify without flags", []string{"verify"}, "", "--in and --sig are required"},
		{"verify missing input", []string{"verify", "--in", missing, "--sig", existing}, "", "no such file"},
		{"verify missing signature", []string{"verify", "--in", existing, "--sig", missing}, "", "no such file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args, &bytes.Buffer{}, envWith(tt.env))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("run(%v) error = %v, want %q", tt.args, err, tt.wantErr)
			}
		})
	}
}

func TestRunRejectsUnknownCommands(t *testing.T) {
	if err := run(nil, &bytes.Buffer{}, envWith("")); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("no command error = %v", err)
	}
	if err := run([]string{"publish"}, &bytes.Buffer{}, envWith("")); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("unknown command error = %v", err)
	}
}
