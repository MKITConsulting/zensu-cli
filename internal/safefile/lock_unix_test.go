//go:build !windows

package safefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTryLockFile_ReportsAFileItCannotLock(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "unit.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if locked, err := tryLockFile(f); locked || err == nil {
		t.Fatalf("a file that cannot be locked must be reported as an error, got locked=%t err=%v", locked, err)
	}
}
