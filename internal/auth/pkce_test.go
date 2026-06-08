package auth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"testing"

	"github.com/MKITConsulting/zensu-cli/internal/auth"
)

var unreserved = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)

func TestGeneratePKCE_VerifierLengthAndCharset(t *testing.T) {
	p, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error: %v", err)
	}
	if n := len(p.Verifier); n < 43 || n > 128 {
		t.Errorf("verifier length %d outside RFC7636 range [43,128]", n)
	}
	if !unreserved.MatchString(p.Verifier) {
		t.Errorf("verifier contains non-unreserved chars: %q", p.Verifier)
	}
}

func TestGeneratePKCE_ChallengeIsS256OfVerifier(t *testing.T) {
	p, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error: %v", err)
	}
	sum := sha256.Sum256([]byte(p.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if p.Challenge != want {
		t.Errorf("challenge: got %q want S256 %q", p.Challenge, want)
	}
	if p.Method() != "S256" {
		t.Errorf("Method(): got %q want S256", p.Method())
	}
}

func TestGeneratePKCE_Unique(t *testing.T) {
	a, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error: %v", err)
	}
	b, err := auth.GeneratePKCE()
	if err != nil {
		t.Fatalf("GeneratePKCE() error: %v", err)
	}
	if a.Verifier == b.Verifier {
		t.Error("two GeneratePKCE() calls produced identical verifiers")
	}
}

func TestGenerateState_RandomNonEmpty(t *testing.T) {
	s1, err := auth.GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() error: %v", err)
	}
	if s1 == "" {
		t.Fatal("GenerateState() returned empty string")
	}
	if !unreserved.MatchString(s1) {
		t.Errorf("state contains non-URL-safe chars: %q", s1)
	}
	s2, err := auth.GenerateState()
	if err != nil {
		t.Fatalf("GenerateState() error: %v", err)
	}
	if s1 == s2 {
		t.Error("two GenerateState() calls produced identical values")
	}
}
