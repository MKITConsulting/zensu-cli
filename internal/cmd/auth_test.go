package cmd

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func authFactory() (*Factory, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Factory{Out: out}, out
}

func TestAuthStatus_LoggedInOAuth(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		APIURL:      "https://api.example.test",
		AccessToken: "acc-1",
		User:        "dev@example.test",
		Org:         "Acme",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"api.example.test", "dev@example.test", "Acme"} {
		if !strings.Contains(got, want) {
			t.Errorf("status missing %q in:\n%s", want, got)
		}
	}
}

func TestAuthStatus_BackfillsIdentityFromJWT(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@zensu.dev","orgName":"Zensu"}`))
	cfg := &config.Config{
		APIURL:      "https://api.example.test",
		AccessToken: "h." + payload + ".sig",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	got := out.String()
	for _, want := range []string{"dev@zensu.dev", "Zensu"} {
		if !strings.Contains(got, want) {
			t.Errorf("status missing %q in:\n%s", want, got)
		}
	}

	after, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.User != "dev@zensu.dev" || after.Org != "Zensu" {
		t.Errorf("identity not persisted after backfill: %+v", after)
	}
}

func TestAuthStatus_OpaqueTokenStaysUnknown(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{
		APIURL:      "https://api.example.test",
		AccessToken: "opaque",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	if !strings.Contains(out.String(), "(unknown user)") {
		t.Errorf("opaque token should stay unknown, got:\n%s", out.String())
	}
}

func TestAuthStatus_PresetUserWinsOverJWT(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"jwt@zensu.dev","orgName":"JwtOrg"}`))
	cfg := &config.Config{
		APIURL:      "https://api.example.test",
		AccessToken: "h." + payload + ".sig",
		User:        "preset@x",
		Org:         "PresetOrg",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "preset@x") || strings.Contains(got, "jwt@zensu.dev") {
		t.Errorf("preset user must win over JWT claims, got:\n%s", got)
	}

	after, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.User != "preset@x" || after.Org != "PresetOrg" {
		t.Errorf("preset identity overwritten by JWT: %+v", after)
	}
}

func TestAuthStatus_BackfillEmailOnlyOmitsOrg(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"solo@zensu.dev"}`))
	cfg := &config.Config{
		APIURL:      "https://api.example.test",
		AccessToken: "h." + payload + ".sig",
		ExpiresAt:   time.Now().Add(10 * time.Minute),
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "solo@zensu.dev") {
		t.Errorf("status missing email, got:\n%s", got)
	}
	if strings.Contains(got, "()") {
		t.Errorf("empty org should not render parenthetical, got:\n%s", got)
	}

	after, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.User != "solo@zensu.dev" || after.Org != "" {
		t.Errorf("backfill persistence wrong: %+v", after)
	}
}

func TestAuthStatus_APIKey(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{APIURL: "https://api.example.test", APIKey: "zsk_secret"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	got := strings.ToLower(out.String())
	if !strings.Contains(got, "api key") {
		t.Errorf("status should indicate API key auth, got:\n%s", out.String())
	}
	if strings.Contains(out.String(), "zsk_secret") {
		t.Error("status must not print the secret API key value")
	}
}

func TestAuthStatus_NotLoggedIn(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "status"); err != nil {
		t.Fatalf("auth status error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out.String()), "not logged in") {
		t.Errorf("expected 'not logged in', got:\n%s", out.String())
	}
}

func TestAuthToken_PrintsAccessToken(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{AccessToken: "acc-xyz", ExpiresAt: time.Now().Add(time.Hour)}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "token"); err != nil {
		t.Fatalf("auth token error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "acc-xyz" {
		t.Errorf("token output: got %q want acc-xyz", out.String())
	}
}

func TestAuthToken_PrintsAPIKey(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	cfg := &config.Config{APIKey: "zsk_key"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "token"); err != nil {
		t.Fatalf("auth token error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "zsk_key" {
		t.Errorf("token output: got %q want zsk_key", out.String())
	}
}

func TestAuthToken_NotLoggedIn(t *testing.T) {
	t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())
	f, _ := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "token"); err == nil {
		t.Fatal("auth token should error when not logged in")
	}
}

func TestAuthLogout_ClearsCredentials(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ZENSU_CONFIG_DIR", dir)
	cfg := &config.Config{APIURL: "https://api.example.test", AccessToken: "acc-1", RefreshToken: "ref-1"}
	if err := cfg.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	f, out := authFactory()
	cmd := NewAuthCmd(f)
	if err := runCmd(t, cmd, "logout"); err != nil {
		t.Fatalf("auth logout error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out.String()), "logged out") {
		t.Errorf("expected 'logged out', got:\n%s", out.String())
	}

	after, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.AccessToken != "" || after.RefreshToken != "" || after.APIKey != "" {
		t.Errorf("credentials not cleared after logout: %+v", after)
	}
}
