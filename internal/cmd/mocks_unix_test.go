//go:build !windows

package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestMocksCreate_ReportsAFileItCannotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions, so os.Open cannot be made to fail this way")
	}
	path := filepath.Join(t.TempDir(), "locked.png")
	if err := os.WriteFile(path, []byte("PNG"), 0o000); err != nil {
		t.Fatalf("writing unreadable fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an unreadable file must be refused before any request is sent")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	err := runCmd(t, NewMocksCmd(f), "create", "f1", path)
	if err == nil {
		t.Fatal("mocks create must refuse a file it cannot open; os.Lstat succeeds on a mode-000 file, so only os.Open catches this")
	}
	if !strings.Contains(err.Error(), "opening mock file") {
		t.Errorf("the pre-open stat succeeds on a mode-000 file, so this must be reported as the open failing and not share a wrapper with the stat, got: %v", err)
	}
}

func TestMocksCreate_RefusesAFifoWithoutBlocking(t *testing.T) {
	pipe := filepath.Join(t.TempDir(), "pipe.png")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("mkfifo is unavailable here: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a FIFO must be refused locally, before any request is sent")
	}))
	defer srv.Close()

	f, _ := testFactory(srv)
	done := make(chan error, 1)
	go func() { done <- runCmd(t, NewMocksCmd(f), "create", "f1", pipe) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("mocks create must refuse a FIFO even when its name ends in an accepted extension")
		}
		if !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("the error should say the path is not a regular file, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mocks create blocked in os.Open on a FIFO; the file mode must be checked before the file is opened")
	}
}
