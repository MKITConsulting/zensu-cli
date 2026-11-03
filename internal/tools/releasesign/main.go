package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MKITConsulting/zensu-cli/internal/update"
)

const keyEnv = "ZENSU_RELEASE_SIGNING_KEY"

var trustedKeys = update.ReleaseKeys

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: releasesign keygen|sign|verify [flags]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "keygen":
		out := flags.String("private-key", "", "path of the new private key file; it must not exist yet")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return keygen(*out, stdout)
	case "sign":
		in := flags.String("in", "", "file to sign")
		out := flags.String("out", "", "signature file to write")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return sign(*in, *out, getenv(keyEnv))
	case "verify":
		in := flags.String("in", "", "signed file")
		sig := flags.String("sig", "", "signature file")
		pub := flags.String("public-key", "", "base64 public key to trust instead of the keys built into zensu")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		return verify(*in, *sig, *pub, stdout)
	default:
		return fmt.Errorf("unknown command %q; want keygen, sign or verify", args[0])
	}
}

func keygen(path string, stdout io.Writer) error {
	if path == "" {
		return errors.New("--private-key is required")
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(priv.Seed())); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(pub))
	return err
}

func sign(in, out, keys string) error {
	if in == "" || out == "" {
		return errors.New("--in and --out are required")
	}
	seeds := strings.Fields(keys)
	if len(seeds) == 0 {
		return fmt.Errorf("%s is not set", keyEnv)
	}
	msg, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var sigs strings.Builder
	for _, s := range seeds {
		seed, err := base64.StdEncoding.DecodeString(s)
		if err != nil || len(seed) != ed25519.SeedSize {
			return fmt.Errorf("%s must hold base64-encoded %d-byte Ed25519 seeds, one per line", keyEnv, ed25519.SeedSize)
		}
		sigs.WriteString(base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.NewKeyFromSeed(seed), msg)) + "\n")
	}
	if err := update.VerifySignature(msg, []byte(sigs.String()), trustedKeys()); err != nil {
		return fmt.Errorf("no key in %s is a release key that zensu trusts (internal/update/keys.go), so zensu update could not verify this release", keyEnv)
	}
	return os.WriteFile(out, []byte(sigs.String()), 0o644)
}

func verify(in, sig, pub string, stdout io.Writer) error {
	if in == "" || sig == "" {
		return errors.New("--in and --sig are required")
	}
	msg, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	sigs, err := os.ReadFile(sig)
	if err != nil {
		return err
	}
	keys := trustedKeys()
	if pub != "" {
		keys = []string{pub}
	}
	if err := update.VerifySignature(msg, sigs, keys); err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, "signature OK")
	return err
}
