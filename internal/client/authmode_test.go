package client

import (
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/config"
)

func TestClientAuthMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"nothing stored", config.Config{}, AuthModeNone},
		{"api key wins over a login", config.Config{APIKey: "zsk_x", AccessToken: "jwt"}, AuthModeAPIKey},
		{"session token", config.Config{AccessToken: SessionTokenPrefix + "abc"}, AuthModeSessionToken},
		{"browser login", config.Config{AccessToken: "eyJhbGciOi", RefreshToken: "r"}, AuthModeLogin},
	}
	for _, tc := range cases {
		cfg := tc.cfg
		if got := New(&cfg, "http://zensu.test", "").AuthMode(); got != tc.want {
			t.Errorf("%s: AuthMode() = %q, want %q", tc.name, got, tc.want)
		}
	}
}
