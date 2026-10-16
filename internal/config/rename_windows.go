package config

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func replaceFile(src, dst string) error {
	return replaceWithRetry(os.Rename, renameBlockedByReader, time.Sleep, src, dst)
}

func renameBlockedByReader(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}
