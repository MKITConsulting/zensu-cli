package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

func TestAIWorkRecoveryOfflineAndLossless(t *testing.T) {
	aiWorkTestRepo(t)
	srv, _ := newFakeAPI(t, nil)
	f, out := testFactory(srv)
	f.NewClient = func(context.Context) (*client.Client, error) {
		t.Fatal("recovery must never contact a server")
		return nil, nil
	}
	session := "scv1_" + strings.Repeat("a", 64)
	gitDir, err := aiWorkGit(context.Background(), "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(gitDir, "zensu-ai-work-v1")
	file := filepath.Join(dir, session+".json")
	raw := []byte("{\n  \"schema\":1,\"id\":\"42a9be6a-a419-40a5-b24d-64c962f23159\",\"pending\":{\"id\":\"891419fa-f7e4-4a52-8123-2bef791876dd\"},\"unknownFutureField\":\"preserve me\"\n}\n")
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	run := func(args ...string) (map[string]any, error) {
		out.Reset()
		err := runCmd(t, NewAIWorkCmd(f), append([]string{"recover", "--session", session}, args...)...)
		var value map[string]any
		if err == nil && json.Unmarshal(out.Bytes(), &value) != nil {
			t.Fatalf("invalid result: %s", out.Bytes())
		}
		return value, err
	}
	got, err := run()
	if err != nil || got["status"] != "not_started" {
		t.Fatalf("empty: %v %v", got, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("inspection created state")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	got, err = run()
	if err != nil || got["active"].(map[string]any)["receipt"] != digest {
		t.Fatalf("inspect: %v %v", got, err)
	}
	for _, args := range [][]string{{"--archive", digest}, {"--archive", strings.Repeat("b", 64), "--confirm"}, {"--archive", digest, "--restore", digest, "--confirm"}, {"--confirm"}} {
		if _, err := run(args...); err == nil {
			t.Fatalf("accepted unsafe options: %v", args)
		}
		if data, _ := os.ReadFile(file); string(data) != string(raw) {
			t.Fatal("rejected command changed receipt")
		}
	}
	if err := os.WriteFile(file+".lock", []byte("busy"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("--archive", digest, "--confirm"); err == nil {
		t.Fatal("ignored capture lock")
	}
	if err := os.Remove(file + ".lock"); err != nil {
		t.Fatal(err)
	}
	got, err = run("--archive", digest, "--confirm")
	if err != nil || got["status"] != "archived" {
		t.Fatalf("archive: %v %v", got, err)
	}
	archive := filepath.Join(dir, "recovery", session, digest+".json")
	if data, _ := os.ReadFile(archive); string(data) != string(raw) {
		t.Fatal("archive changed original bytes")
	}
	if st, _ := os.Stat(archive); runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
		t.Fatal("archive is not private")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("active slot not released")
	}
	got, err = run()
	if err != nil || len(got["archives"].([]any)) != 1 {
		t.Fatalf("list: %v %v", got, err)
	}
	got, err = run("--restore", digest, "--confirm")
	if err != nil || got["status"] != "restored" {
		t.Fatalf("restore: %v %v", got, err)
	}
	restored, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]json.RawMessage
	if json.Unmarshal(raw, &before) != nil || json.Unmarshal(restored, &after) != nil {
		t.Fatal("invalid restored JSON")
	}
	if string(after["capturePaused"]) != "true" {
		t.Fatal("restored receipt could automatically submit at Stop")
	}
	delete(after, "capturePaused")
	for key, value := range before {
		if string(after[key]) != string(value) {
			t.Fatalf("changed original value %s", key)
		}
	}
	if len(after) != len(before) {
		t.Fatal("lost unknown receipt fields")
	}
	if data, _ := os.ReadFile(archive); string(data) != string(raw) {
		t.Fatal("changed original archive bytes")
	}
	restoredDigest := got["active"].(map[string]any)["receipt"].(string)
	if _, err = run("--restore", digest, "--confirm"); err == nil {
		t.Fatal("restoration overwrote an active goal")
	}
	if _, err = run("--archive", restoredDigest, "--confirm"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(archive, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = run("--restore", digest, "--confirm"); err == nil {
		t.Fatal("restored corrupt archive")
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("failed restoration wrote active file")
	}
}

func TestAIWorkRecoveryRejectsUnsafeStorage(t *testing.T) {
	aiWorkTestRepo(t)
	srv, _ := newFakeAPI(t, nil)
	f, _ := testFactory(srv)
	for _, key := range []string{"", "../outside", "scv1_short", "awcv1_" + strings.Repeat("g", 64)} {
		if runCmd(t, NewAIWorkCmd(f), "recover", "--session", key) == nil {
			t.Fatalf("accepted key %q", key)
		}
	}
	gitDir, _ := aiWorkGit(context.Background(), "rev-parse", "--absolute-git-dir")
	dir := filepath.Join(gitDir, "zensu-ai-work-v1")
	outside := t.TempDir()
	if err := os.Symlink(outside, dir); err != nil {
		t.Fatal(err)
	}
	key := "awcv1_" + strings.Repeat("a", 64)
	if runCmd(t, NewAIWorkCmd(f), "recover", "--session", key) == nil {
		t.Fatal("followed state directory symlink")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, key+".json")
	if err := os.Symlink(filepath.Join(outside, "secret"), file); err != nil {
		t.Fatal(err)
	}
	if runCmd(t, NewAIWorkCmd(f), "recover", "--session", key) == nil {
		t.Fatal("followed dangling receipt symlink")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("x", 98305)), 0600); err != nil {
		t.Fatal(err)
	}
	if runCmd(t, NewAIWorkCmd(f), "recover", "--session", key) == nil {
		t.Fatal("read oversized receipt")
	}
	if err := os.WriteFile(file, []byte("invalid JSON remains recoverable"), 0600); err != nil {
		t.Fatal(err)
	}
	if runCmd(t, NewAIWorkCmd(f), "recover", "--session", key) != nil {
		t.Fatal("cannot inspect malformed legacy receipt")
	}
	if err := os.Symlink(outside, filepath.Join(dir, "recovery")); err != nil {
		t.Fatal(err)
	}
	if runCmd(t, NewAIWorkCmd(f), "recover", "--session", key) == nil {
		t.Fatal("followed archive directory symlink")
	}
}

func TestAIWorkRecoveryDescriptionsAndFailureBoundaries(t *testing.T) {
	for _, tc := range []struct{ scope, pending, reason string }{
		{`null`, `null`, "legacy_product_scope"},
		{`{}`, `null`, "legacy_feature_scope"},
		{`{"taskScope":"feature"}`, `{"id":"frozen"}`, "pending"},
		{`{"taskScope":"maintenance"}`, `null`, "ready"},
	} {
		raw := []byte(`{"schema":1,"id":"42a9be6a-a419-40a5-b24d-64c962f23159","scope":` + tc.scope + `,"pending":` + tc.pending + `}`)
		if got := captureDescribe(raw); got.Reason != tc.reason {
			t.Fatalf("description: %+v", got)
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "receipt.json")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if capturePreserve(file, []byte("replacement")) == nil {
		t.Fatal("overwrote archive collision")
	}
	if raw, _ := os.ReadFile(file); string(raw) != "original" {
		t.Fatal("original was lost")
	}
	if capturePreserve(dir, []byte("replacement")) == nil {
		t.Fatal("accepted directory as archive")
	}
	if capturePublish(filepath.Join(dir, "missing", "receipt.json"), []byte("data")) == nil {
		t.Fatal("ignored missing parent")
	}
	if _, err := captureRead(filepath.Join(file, "child")); err == nil {
		t.Fatal("ignored invalid ancestor")
	}
	if captureDirectory(filepath.Join(file, "child"), false) == nil {
		t.Fatal("ignored invalid directory ancestor")
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		if err := os.Chmod(file, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(file, 0600)
		if _, err := captureRead(file); err == nil {
			t.Fatal("read inaccessible receipt")
		}
	}
}

func TestAIWorkRecoveryArchiveIntegrityAndIsolation(t *testing.T) {
	aiWorkTestRepo(t)
	srv, _ := newFakeAPI(t, nil)
	f, out := testFactory(srv)
	key := "scv1_" + strings.Repeat("e", 64)
	gitDir, _ := aiWorkGit(context.Background(), "rev-parse", "--absolute-git-dir")
	dir := filepath.Join(gitDir, "zensu-ai-work-v1")
	archiveDir := filepath.Join(dir, "recovery", key)
	if err := os.MkdirAll(archiveDir, 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema":1,"id":"42a9be6a-a419-40a5-b24d-64c962f23159"}`)
	digest := captureDigest(raw)
	file := filepath.Join(dir, key+".json")
	archive := filepath.Join(archiveDir, digest+".json")
	run := func(args ...string) error {
		out.Reset()
		return runCmd(t, NewAIWorkCmd(f), append([]string{"recover", "--session", key}, args...)...)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if run("--archive", digest, "--confirm") == nil {
		t.Fatal("archived into a conflicting destination")
	}
	if data, _ := os.ReadFile(file); string(data) != string(raw) {
		t.Fatal("lost active bytes on collision")
	}
	if run() == nil {
		t.Fatal("inspection hid a corrupt archive")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(archive, 0700); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Fatal("inspection accepted a directory receipt")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if run("--restore", digest, "--confirm") == nil {
		t.Fatal("restored directory receipt")
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if run("--restore", digest, "--confirm") == nil {
		t.Fatal("restored missing receipt")
	}
	if err := os.WriteFile(filepath.Join(archiveDir, ".receipt-interrupted.tmp"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "unrelated.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), digest) {
		t.Fatal("listed temporary or unrelated files as archives")
	}
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		if err := os.Chmod(archiveDir, 0000); err != nil {
			t.Fatal(err)
		}
		if run() == nil {
			t.Fatal("hid an inaccessible archive directory")
		}
		if err := os.Chmod(archiveDir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(file, 0700); err != nil {
		t.Fatal(err)
	}
	if run("--archive", digest, "--confirm") == nil {
		t.Fatal("archived directory receipt")
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if run() == nil {
		t.Fatal("recovery accepted a non-repository")
	}
}

func TestAIWorkRecoveryPausedCopy(t *testing.T) {
	for _, raw := range []string{"broken JSON", "null", "[]", "true"} {
		got, err := capturePausedCopy([]byte(raw))
		if err != nil || string(got) != raw {
			t.Fatalf("changed opaque malformed receipt: %q %v", got, err)
		}
	}
	raw := []byte(`{"future":9007199254740993123456789,"capturePaused":false}`)
	got, err := capturePausedCopy(raw)
	if err != nil || !strings.Contains(string(got), `"future":9007199254740993123456789`) || !strings.Contains(string(got), `"capturePaused":true`) {
		t.Fatalf("unsafe working copy: %s %v", got, err)
	}
	oversize := []byte(`{"future":"` + strings.Repeat("x", captureReceiptLimit-13) + `"}`)
	if _, err := capturePausedCopy(oversize); err == nil {
		t.Fatal("accepted oversized paused copy")
	}
}
