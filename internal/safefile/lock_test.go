package safefile_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/safefile"
)

func TestLock_ExcludesASecondHolderUntilReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unit.lock")

	unlock, err := safefile.Lock(context.Background(), path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := safefile.Lock(ctx, path); !errors.Is(err, safefile.ErrLockBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while the first is held: got %v, want ErrLockBusy wrapping the deadline", err)
	}
	unlock()

	again, err := safefile.Lock(context.Background(), path)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("lock file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("lock file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLock_WaitsForTheHolderToRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unit.lock")
	unlock, err := safefile.Lock(context.Background(), path)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(80 * time.Millisecond)
		close(released)
		unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second, err := safefile.Lock(ctx, path)
	if err != nil {
		t.Fatalf("a waiter must get the lock once the holder releases it: %v", err)
	}
	defer second()
	select {
	case <-released:
	default:
		t.Error("the waiter got the lock while the first holder still held it")
	}
}

func TestLock_ReportsADirectoryThatDoesNotExist(t *testing.T) {
	if _, err := safefile.Lock(context.Background(), filepath.Join(t.TempDir(), "missing", "unit.lock")); err == nil {
		t.Fatal("a lock in a missing directory must fail instead of creating the directory")
	}
}

func TestReplace_SwapsTheTargetForTheNewContent(t *testing.T) {
	dir := t.TempDir()
	target, fresh := filepath.Join(dir, "state.json"), filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fresh, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Replace(fresh, target); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("target after replace = %q, %v", got, err)
	}
	if _, err := os.Stat(fresh); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the source must be gone after the replace, stat err = %v", err)
	}
}
