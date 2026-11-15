//go:build !unix

package cmd

import "os"

func openValueFile(path string) (*os.File, error) {
	return os.Open(path)
}
