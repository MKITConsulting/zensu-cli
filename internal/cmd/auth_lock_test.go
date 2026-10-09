package cmd

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

const lockedWindow = 150 * time.Millisecond

func holdSessionLock(t *testing.T) func() {
	t.Helper()
	unlock, err := config.LockSession(context.Background())
	if err != nil {
		t.Fatalf("holding the session lock: %v", err)
	}
	return unlock
}

func runAuthInBackground(t *testing.T, args ...string) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		f, _ := authFactory()
		done <- runCmd(t, NewAuthCmd(f), args...)
	}()
	return done
}

func requireStillWaiting(t *testing.T, done <-chan error, verb string) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("auth %s finished while another process held the session lock (err %v)", verb, err)
	case <-time.After(lockedWindow):
	}
}

func storedLogin(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading the stored login: %v", err)
	}
	return cfg
}

func TestAuthLogout_WaitsForTheSessionLockBeforeClearing(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	login := &config.Config{APIURL: "https://api.example.test", AccessToken: "acc-1", RefreshToken: "ref-1"}
	if err := login.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	unlock := holdSessionLock(t)
	done := runAuthInBackground(t, "logout")
	requireStillWaiting(t, done, "logout")
	if held := storedLogin(t); held.RefreshToken != "ref-1" {
		t.Fatalf("logout wrote the store while the lock was held: %+v", held)
	}
	unlock()

	if err := <-done; err != nil {
		t.Fatalf("logout after the lock was released: %v", err)
	}
	if after := storedLogin(t); after.AccessToken != "" || after.RefreshToken != "" || after.APIURL != login.APIURL {
		t.Errorf("logout result = %+v, want the host kept and every credential cleared", after)
	}
}

func TestAuthStatus_BackfillLeavesATokenRotatedMeanwhileAlone(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@zensu.dev","orgName":"Zensu"}`))
	before := &config.Config{
		APIURL:       "https://api.example.test",
		AccessToken:  "h." + claims + ".old",
		RefreshToken: "ref-1",
		ExpiresAt:    time.Now().Add(10 * time.Minute),
	}
	if err := before.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	unlock := holdSessionLock(t)
	done := runAuthInBackground(t, "status")
	requireStillWaiting(t, done, "status")
	if held := storedLogin(t); held.User != "" || held.AccessToken != before.AccessToken {
		t.Fatalf("status wrote the store while the lock was held: %+v", held)
	}
	rotated := &config.Config{
		APIURL:       before.APIURL,
		AccessToken:  "h." + claims + ".new",
		RefreshToken: "ref-2",
		ExpiresAt:    time.Now().Add(15 * time.Minute),
	}
	if err := rotated.Save(); err != nil {
		t.Fatalf("rotating under the lock: %v", err)
	}
	unlock()

	if err := <-done; err != nil {
		t.Fatalf("status: %v", err)
	}
	after := storedLogin(t)
	if after.AccessToken != rotated.AccessToken || after.RefreshToken != "ref-2" {
		t.Errorf("status put the pre-rotation login back: %+v", after)
	}
}

func TestAuthStatus_GivesUpTheBackfillWhenTheLockStaysBusy(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@zensu.dev","orgName":"Zensu"}`))
	login := &config.Config{
		APIURL:      "https://api.example.test",
		AccessToken: "h." + claims + ".sig",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := login.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	unlock := holdSessionLock(t)
	defer unlock()

	start := time.Now()
	done := runAuthInBackground(t, "status")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("status: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("status blocked on a busy session lock instead of skipping the identity backfill")
	}
	if took := time.Since(start); took > 5*identityBackfillWait {
		t.Errorf("status took %v with the lock busy, want about the %v backfill budget", took, identityBackfillWait)
	}
	if after := storedLogin(t); after.User != "" {
		t.Errorf("status backfilled the identity without the lock: %+v", after)
	}
}

func TestLoginWithToken_WaitsForTheSessionLockBeforeStoring(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "zsk_valid" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	unlock := holdSessionLock(t)
	done := runAuthInBackground(t, "login", "--api-url", srv.URL, "--with-token", "zsk_valid")
	requireStillWaiting(t, done, "login")
	if held := storedLogin(t); held.APIKey != "" {
		t.Fatalf("login wrote the store while the lock was held: %+v", held)
	}
	unlock()

	if err := <-done; err != nil {
		t.Fatalf("login after the lock was released: %v", err)
	}
	if after := storedLogin(t); after.APIKey != "zsk_valid" || after.APIURL != srv.URL {
		t.Errorf("login result = %+v, want the API key stored for %s", after, srv.URL)
	}
}
