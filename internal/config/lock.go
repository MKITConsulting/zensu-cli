package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/safefile"
)

const (
	sessionLockFileName = configFileName + ".lock"
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
	unlock, err := safefile.Lock(ctx, filepath.Join(dir, sessionLockFileName))
	if errors.Is(err, safefile.ErrLockBusy) {
		return nil, fmt.Errorf("%w: %w", ErrSessionLockBusy, ctx.Err())
	}
	return unlock, err
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
