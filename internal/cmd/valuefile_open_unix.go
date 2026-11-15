//go:build unix

package cmd

import (
	"os"
	"syscall"
)

func openValueFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOCTTY, 0)
}
