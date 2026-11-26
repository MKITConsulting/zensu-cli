package safefile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLock_ReportsALockItCannotTake(t *testing.T) {
	broken := errors.New("the lock call failed")
	original := tryLock
	tryLock = func(*os.File) (bool, error) { return false, broken }
	t.Cleanup(func() { tryLock = original })

	path := filepath.Join(t.TempDir(), "unit.lock")
	unlock, err := Lock(context.Background(), path)
	if unlock != nil || !errors.Is(err, broken) || errors.Is(err, ErrLockBusy) {
		t.Fatalf("a failing lock call must be reported as it is, not as a busy lock: %v", err)
	}
}
