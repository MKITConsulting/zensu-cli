//go:build !windows

package cmd

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestValueFiles_RefuseAFileTheyCannotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions, so os.Open cannot be made to fail this way")
	}
	path := filepath.Join(t.TempDir(), "locked.md")
	if err := os.WriteFile(path, []byte("Confidential findings.\n"), 0o000); err != nil {
		t.Fatalf("writing unreadable fixture: %v", err)
	}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{})
	f, _ := testFactory(srv)
	err := runCmd(t, NewFeaturesCmd(f), "update", "f1", "--description-file", path)
	if err == nil || !errors.Is(err, fs.ErrPermission) || !strings.HasPrefix(err.Error(), "--description-file: cannot read "+path+": ") {
		t.Fatalf("err = %v, want the open failure for %s", err, path)
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("sent %d requests: %v", len(calls), api.summary())
	}
}

func TestValueFiles_RefuseAFifoWithoutBlocking(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe.md")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("mkfifo is unavailable here: %v", err)
	}
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{})
	f, _ := testFactory(srv)
	done := make(chan error, 1)
	go func() { done <- runCmd(t, NewFeaturesCmd(f), "update", "f1", "--description-file", pipe) }()
	select {
	case err := <-done:
		if err == nil || err.Error() != "--description-file: "+pipe+" is not a regular file" {
			t.Fatalf("err = %v, want the FIFO refused as not a regular file", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked; open value files without blocking and check the opened file")
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("sent %d requests: %v", len(calls), api.summary())
	}
}

func TestValueFiles_RefuseADevice(t *testing.T) {
	srv, api := newFakeAPI(t, map[string]func(recordedRequest) fakeReply{})
	f, _ := testFactory(srv)
	err := runCmd(t, NewFeaturesCmd(f), "update", "f1", "--description-file", os.DevNull)
	if err == nil || err.Error() != "--description-file: "+os.DevNull+" is not a regular file" {
		t.Fatalf("err = %v, want %s refused as not a regular file", err, os.DevNull)
	}
	if calls := api.calls(); len(calls) != 0 {
		t.Fatalf("sent %d requests: %v", len(calls), api.summary())
	}
}
