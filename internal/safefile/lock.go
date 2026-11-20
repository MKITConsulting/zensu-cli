package safefile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const (
	lockFilePerm = 0o600
	lockPoll     = 25 * time.Millisecond
)

var ErrLockBusy = errors.New("another zensu process holds the lock")

var tryLock = tryLockFile

func Lock(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, lockFilePerm)
	if err != nil {
		return nil, err
	}
	for {
		locked, err := tryLock(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", f.Name(), err)
		}
		if locked {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		wait := time.NewTimer(lockPoll)
		select {
		case <-ctx.Done():
			wait.Stop()
			f.Close()
			return nil, fmt.Errorf("%w: %w", ErrLockBusy, ctx.Err())
		case <-wait.C:
		}
	}
}
