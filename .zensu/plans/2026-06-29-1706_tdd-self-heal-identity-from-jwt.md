# TDD Plan: CLI self-heals user identity from JWT access token

## Context
Feature: CLI self-heals user identity from JWT access token (status backfill + refresh update).

Root cause: `zensu auth status` shows "(unknown user)" because identity (email/org) is derived ONLY at browser-login time by JWT-decoding the access token (`internal/cmd/login.go:124 identityFromToken`, stored at login.go:110-111). Even after the server switches to JWT access tokens carrying `email`+`orgName`, the CLI never re-derives identity except at fresh login, so current sessions stay "(unknown user)" until logout+re-login and `cfg.Org` goes stale. Harden the CLI to exploit the JWT everywhere and self-heal. Server-side token format change is OUT OF SCOPE (separate repo).

**Approach**: Vanilla implementation (TDD discipline disabled via hooks.tddImplementation) | **Tech Stack**: Go 1.26, cobra | **Coverage**: `go test -coverprofile=cover.out ./... && go tool cover -func=cover.out` @ 90% (default-90%)

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` | present | n/a |

## Cross-Layer Value Flow Pairings
(Vanilla mode — pairing analysis not applicable. No rows.)

## Status Legend
| [ ] Not started | [I] Implemented | [G] GREEN | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts |
|------|------|-------------|-----------|------------|--------|----------|
| S1 | Refactoring | Extract `identityFromToken` → `auth.IdentityFromToken`; update login.go; migrate test | internal/auth/identity_test.go | - | [G] | 1 |
| S2 | Feature | `auth status` offline backfill: decode stored token when cfg.User empty, display + persist | internal/cmd/auth_test.go | S1 | [G] | 1 |
| S3 | Feature | `client.refresh()` updates cfg.User/cfg.Org from refreshed JWT | internal/client/client_test.go | S1 | [G] | 1 |

### Step S1 — Shared identity extractor
- Add `internal/auth/identity.go` with `func IdentityFromToken(token string) (email, org string)` (3-part JWT, base64 RawURL decode payload, unmarshal `{email, orgName}`, "","" on any failure).
- Rewrite `internal/cmd/login.go` `identityFromToken` callsite to `auth.IdentityFromToken`; delete the local func.
- Migrate `TestIdentityFromToken` from `internal/cmd/login_test.go` to `internal/auth/identity_test.go`; drop the now-unused `encoding/base64` import in login_test.go.

### Step S2 — status backfill
- In `newAuthStatusCmd` AccessToken branch: when `cfg.User == ""`, `email, org := auth.IdentityFromToken(cfg.AccessToken)`; if email != "" → use for display, set cfg.User/cfg.Org, best-effort `cfg.Save()`. Opaque token → keep "(unknown user)". Fully offline.

### Step S3 — refresh identity update
- In `client.refresh()`, after successful token fetch and before `c.save`: `email, org := auth.IdentityFromToken(tok.AccessToken)`; if email != "" → set c.cfg.User/c.cfg.Org. Opaque refresh never wipes existing identity.

**Checkpoint**: `go build ./...` + `go vet ./...` + `go test ./...` pass

## Final Verification
- All test suites pass
- Coverage report generated for changed files (threshold: 90%)
