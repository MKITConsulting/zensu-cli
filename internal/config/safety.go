package config

import (
	"fmt"
	"path/filepath"
	"testing"
)

func GuardRealDirWrite(dir string) error {
	if !testing.Testing() {
		return nil
	}
	real, err := UserConfigDir()
	if err != nil {
		return nil
	}
	if filepath.Clean(dir) != filepath.Clean(real) {
		return nil
	}
	return fmt.Errorf("refusing to write the real zensu config dir %s from a test binary: point ZENSU_CONFIG_DIR at a temporary directory (see internal/testutil.RunWithIsolatedConfigDir)", dir)
}
