package config_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/config"
	"github.com/MKITConsulting/zensu-cli/internal/testutil"
)

func TestLockSession_ExcludesASecondHolderUntilReleased(t *testing.T) {
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)

	unlock, err := config.LockSession(context.Background())
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := config.LockSession(ctx); !errors.Is(err, config.ErrSessionLockBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second lock while the first is held: got %v, want ErrSessionLockBusy wrapping the deadline", err)
	}
	unlock()

	again, err := config.LockSession(context.Background())
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again()

	info, err := os.Stat(filepath.Join(dir, "hosts.json.lock"))
	if err != nil {
		t.Fatalf("lock file: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("lock file mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLockSession_WaitsForTheHolderToRelease(t *testing.T) {
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)

	unlock, err := config.LockSession(context.Background())
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	const hold = 120 * time.Millisecond
	released := make(chan struct{})
	go func() {
		time.Sleep(hold)
		unlock()
		close(released)
	}()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	second, err := config.LockSession(ctx)
	if err != nil {
		t.Fatalf("waiting lock: %v", err)
	}
	waited := time.Since(start)
	second()
	<-released
	if waited < hold/2 {
		t.Errorf("second holder got the lock after %v, before the first released it at %v", waited, hold)
	}
}

func TestLockSession_RefusesTheRealCredentialStoreUnderTest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AppData", home)

	pretendReal, err := config.UserConfigDir()
	if err != nil {
		t.Fatalf("config.UserConfigDir: %v", err)
	}
	if !strings.HasPrefix(filepath.Clean(pretendReal), filepath.Clean(home)) {
		t.Fatalf("home redirection did not take: UserConfigDir is %q, not under %q — refusing to run this test against the real store", pretendReal, home)
	}
	t.Setenv("ZENSU_CONFIG_DIR", pretendReal)

	if _, err := config.LockSession(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to write the real zensu config dir") {
		t.Fatalf("LockSession from a test binary against the user config dir: got %v, want the guard refusal", err)
	}
	if _, err := os.Stat(filepath.Join(pretendReal, "hosts.json.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused lock must not create the lock file, stat err = %v", err)
	}
}

func TestUpdateStored_ChangesTheLatestStoredLoginUnderTheLock(t *testing.T) {
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)
	initial := config.Config{APIURL: "https://api.example.test", AccessToken: "acc-1", RefreshToken: "ref-1"}
	if err := initial.Save(); err != nil {
		t.Fatalf("storing the login: %v", err)
	}

	if err := config.UpdateStored(context.Background(), func(stored *config.Config) bool {
		if stored.RefreshToken != "ref-1" {
			t.Errorf("update saw refresh token %q, want the stored ref-1", stored.RefreshToken)
		}
		stored.SetIdentity("dev@example.test", "Acme")
		return true
	}); err != nil {
		t.Fatalf("UpdateStored: %v", err)
	}
	got, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.User != "dev@example.test" || got.Org != "Acme" || got.RefreshToken != "ref-1" {
		t.Errorf("stored login after update = %+v", got)
	}

	if err := config.UpdateStored(context.Background(), func(stored *config.Config) bool {
		stored.RefreshToken = "must-not-be-written"
		return false
	}); err != nil {
		t.Fatalf("UpdateStored without a change: %v", err)
	}
	if got, _ := config.Load(); got.RefreshToken != "ref-1" {
		t.Errorf("an update that declined still wrote refresh token %q", got.RefreshToken)
	}
}

func TestUpdateStored_WaitsForTheSessionLock(t *testing.T) {
	dir := testutil.RequireIsolatedConfigDir(t)
	testutil.ClearIsolatedConfigDir(t, dir)
	unlock, err := config.LockSession(context.Background())
	if err != nil {
		t.Fatalf("holding the lock: %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	called := false
	err = config.UpdateStored(ctx, func(*config.Config) bool {
		called = true
		return true
	})
	if !errors.Is(err, config.ErrSessionLockBusy) {
		t.Fatalf("UpdateStored while another process holds the lock: got %v, want ErrSessionLockBusy", err)
	}
	if called {
		t.Error("the update ran without holding the lock")
	}
}
