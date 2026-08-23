package update

import (
	"os"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

func TestMain(m *testing.M) {
	os.Exit(testutil.RunWithIsolatedConfigDir(m.Run))
}

func TestConfigDir_IsIsolatedFromRealCredentialStore(t *testing.T) {
	testutil.RequireIsolatedConfigDir(t)
}
