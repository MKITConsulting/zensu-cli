package auth_test

import (
	"encoding/base64"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
)

func TestIdentityFromToken(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"a@b.c","orgName":"Acme"}`))
	tok := "h." + payload + ".sig"
	email, org := auth.IdentityFromToken(tok)
	if email != "a@b.c" || org != "Acme" {
		t.Errorf("IdentityFromToken = (%q,%q), want (a@b.c, Acme)", email, org)
	}

	if e, o := auth.IdentityFromToken("not-a-jwt"); e != "" || o != "" {
		t.Errorf("malformed token should yield empty, got (%q,%q)", e, o)
	}

	if e, o := auth.IdentityFromToken("h.!!!not-base64!!!.sig"); e != "" || o != "" {
		t.Errorf("undecodable payload should yield empty, got (%q,%q)", e, o)
	}

	notJSON := base64.RawURLEncoding.EncodeToString([]byte("plain text"))
	if e, o := auth.IdentityFromToken("h." + notJSON + ".sig"); e != "" || o != "" {
		t.Errorf("non-JSON payload should yield empty, got (%q,%q)", e, o)
	}
}

func TestIdentityFromToken_PartialClaims(t *testing.T) {
	emailOnly := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"a@b.c"}`))
	if e, o := auth.IdentityFromToken("h." + emailOnly + ".sig"); e != "a@b.c" || o != "" {
		t.Errorf("email-only = (%q,%q), want (a@b.c, \"\")", e, o)
	}

	orgOnly := base64.RawURLEncoding.EncodeToString([]byte(`{"orgName":"Acme"}`))
	if e, o := auth.IdentityFromToken("h." + orgOnly + ".sig"); e != "" || o != "Acme" {
		t.Errorf("org-only = (%q,%q), want (\"\", Acme)", e, o)
	}
}

func TestIdentityFromToken_StripsControlChars(t *testing.T) {
	raw := "{\"email\":\"a@b.c\\u001b[31mX\",\"orgName\":\"Ac\\u0007me\"}"
	payload := base64.RawURLEncoding.EncodeToString([]byte(raw))
	email, org := auth.IdentityFromToken("h." + payload + ".sig")
	if email != "a@b.c[31mX" || org != "Acme" {
		t.Errorf("control chars not stripped: email=%q org=%q", email, org)
	}
}
