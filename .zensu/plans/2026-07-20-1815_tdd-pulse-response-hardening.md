# TDD Plan: Harden Pulse response validation

## Context
Resolve every open finding on zensu-cli PR #15 before merge. The privacy-sensitive Pulse command boundary must reject the nil UUID as a session identity for server responses and `end`/`summary` arguments. It must also reject duplicate top-level `id` or `status` response keys case-insensitively so a contradictory privacy response cannot be overridden by Go's permissive JSON decoding.

**Approach**: Vanilla implementation (TDD discipline disabled via hooks.tddImplementation) | **Tech Stack**: Go 1.26, Cobra, encoding/json | **Coverage**: `go test -coverprofile=cover.out ./... && go tool cover -func=cover.out` @ 90% (default-90%)

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` | present | n/a |
| gh | CLI | `gh auth status` | present | n/a |
| zensu | CLI | `zensu auth status` | present | n/a |

## Cross-Layer Value Flow Pairings
(Vanilla mode — pairing analysis not applicable. No new value crosses an unchanged process, persistence, or transport layer.)

## Status Legend
| [ ] Not started | [I] Implemented | [G] GREEN | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts |
|------|------|-------------|-----------|------------|--------|----------|
| S1 | Bug Fix | Reject nil session UUIDs and duplicate case-insensitive `id`/`status` keys at the Pulse CLI boundary | `internal/cmd/pulse_test.go` | — | [I] | 1 |

### Step S1 — Strict Pulse identity and envelope validation
- Reject `00000000-0000-0000-0000-000000000000` both when received from the server and when supplied to `pulse end` or `pulse summary`.
- Inspect top-level JSON object keys before decoding and reject repeated `id` or `status` keys, including case variants.
- Preserve existing acceptance and redaction behavior for valid enabled and tracking-disabled responses.

**Checkpoint**: `make test` + `make vet` + `make build` pass

## Final Verification
- Full race-enabled test suite passes
- Vet and canonical build pass
- Coverage for `internal/cmd/pulse.go` remains at or above 90%
- Both GitHub review threads are replied to and resolved after the fix is pushed
