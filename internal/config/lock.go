package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	sessionLockFileName = configFileName + ".lock"
	sessionLockPoll     = 25 * time.Millisecond
	SessionLockWait     = time.Minute
)

var ErrSessionLockBusy = errors.New("another zensu process is updating the stored login")

func LockSession(ctx context.Context) (func(), error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	if err := GuardRealDirWrite(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, sessionLockFileName), os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, err
	}
	for {
		locked, err := tryLockFile(f)
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
		wait := time.NewTimer(sessionLockPoll)
		select {
		case <-ctx.Done():
			wait.Stop()
			f.Close()
			return nil, fmt.Errorf("%w: %w", ErrSessionLockBusy, ctx.Err())
		case <-wait.C:
		}
	}
}

func UpdateStored(ctx context.Context, update func(*Config) bool) error {
	lockCtx, cancel := context.WithTimeout(ctx, SessionLockWait)
	unlock, err := LockSession(lockCtx)
	cancel()
	if err != nil {
		return err
	}
	defer unlock()
	stored, err := Load()
	if err != nil {
		return err
	}
	if !update(stored) {
		return nil
	}
	return stored.Save()
}
