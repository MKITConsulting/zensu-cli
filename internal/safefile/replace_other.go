//go:build !windows

package safefile

import "os"

func replaceFile(src, dst string) error {
	return os.Rename(src, dst)
}
