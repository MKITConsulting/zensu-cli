# TDD Plan: Test isolation for internal/client (zensu-cli credential store)

## Context

Feature: Test isolation in the `internal/client` package of the zensu CLI — no test may overwrite the developer's real credential store.

**Work tree (NOT this monorepo):**
`/Users/marcelkarras/IdeaProjects/dev.zensu/zensu-cli/.claude/worktrees/client-test-config-isolation`
Branch `claude/client-test-config-isolation`, freshly branched from `origin/main` (`bd5a84b`). Worktree created in this session, clean, no foreign changes. Repo language: English.

**Artifact anchoring note.** This TDD session's Zensu artifacts (this plan, the run log, the witness log, the FSM state) are anchored to the *monorepo* worktree, because that is this Claude Code session's project dir and the PostToolUse witness hook resolves there (`witness-scv1_e52f56b5….log`, verified present). The code change itself lands in the zensu-cli worktree above. The Phase 6 Edit Landing Audit is therefore run with `--project` pointed at the zensu-cli worktree.

**Problem (verified in this session):**

- `client.New()` (`internal/client/client.go:58`) installs the default saver `func(cf *config.Config) error { return cf.Save() }`.
- `config.ConfigDir()` (`internal/config/config.go:31-50`) falls back to `$HOME/.config/zensu` when neither `ZENSU_CONFIG_DIR` nor `XDG_CONFIG_HOME` is set; the file is `hosts.json` (`configFileName`, `config.go:16`).
- The `internal/client` package sets `ZENSU_CONFIG_DIR` nowhere and has no `TestMain` — verified by grep over `internal/client/` on fresh `origin/main`: zero hits. The packages `internal/cmd`, `internal/config` and `internal/update` do isolate correctly via `t.Setenv("ZENSU_CONFIG_DIR", t.TempDir())`.
- `TestDo_RetriesOnce401ThenRefresh` (`internal/client/client_test.go:213`) builds the client WITHOUT `client.WithSaver(...)` and performs a SUCCESSFUL refresh. `Client.refresh()` ends in `return c.save(c.cfg)`, so the default saver writes the real `~/.config/zensu/hosts.json`.
- Observed real damage: the user's real file contained `accessToken="fresh"`, `refreshToken="r2"`, `expiresAt` = write time + 900s — exactly the fixture at `internal/client/client_test.go:200`. Consequence: every `zensu` command failed with `refreshing session: token endpoint: invalid_grant: refresh token is invalid or expired`, sending the user into a `zensu auth login` loop.
- Scope note: all other refresh-carrying tests in `internal/client` already stub the saver. The remaining `WithSaver`-less tests (`TestDo_InjectsBearer`, `TestDo_InjectsAPIKey`, `TestCheckResponse_ParsesAPIError`, `TestCheckResponse_NonJSONBodyFallback`, `TestDo_RefreshFailsWhenNoRefreshToken`) never reach a successful refresh and therefore do not save today — but they sit in the same latent trap.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.26.3, stdlib `testing`, `net/http/httptest`, cobra CLI | **Coverage**: `go test -coverprofile=cover.out ./... && go tool cover -func=cover.out` @ 90% lines (default-90%)

## Requirements

| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | Running the full test suite leaves `~/.config/zensu/hosts.json` byte-identical — size, mtime and SHA-256 unchanged before vs. after | spec |
| AC-002 | `go build ./...`, `go vet ./...` and `go test -race ./...` pass in the worktree | spec |
| FR-001 | The `internal/client` package points `ZENSU_CONFIG_DIR` at a throwaway directory for the entire test binary run, so any test — existing or future — that reaches the default saver is sandboxed | spec |
| FR-002 | `TestDo_RetriesOnce401ThenRefresh` stubs the saver explicitly, in the same style as its already-correct sibling tests in the same file | spec |
| FR-003 | A regression test fails when the isolation is removed; it asserts the resolved config directory and the file actually written, not the presence of a setup call | spec |
| FR-004 | No production-code change; `client.New()`'s default saver stays intact because the real CLI depends on it | spec |
| FR-005 | The isolation lives in one shared, importable place rather than being hand-copied per package | round-2 security review (SUGGESTION) |
| FR-006 | `internal/cmd` is isolated too — it carries the identical latent trap, dormant only because one fixture pins API-key mode | round-2 security review (CRITICAL) |

## Preconditions

| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` → `go version go1.26.3 darwin/arm64` | present | install |
| node | CLI | `command -v node` → `v23.11.0` | present | install |

## Cross-Layer Value Flow Pairings

(No pairings. The change is confined to test files in a single Go package; no new value, field, payload key or query parameter crosses a process, persistence or network boundary.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps

| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Package-wide `TestMain` that redirects `ZENSU_CONFIG_DIR` to a throwaway dir for the whole `internal/client` test binary | `internal/client/isolation_test.go` | – | [I] | 1 | FR-001, AC-001 |
| S2 | Feature | Regression tests: resolved config dir is not the real credential dir, and a real default-saver refresh writes inside the sandbox | `internal/client/isolation_test.go` | S1 | [I] | 2 | FR-003, AC-001 |
| S3 | Feature | `TestDo_RetriesOnce401ThenRefresh` gets an explicit `client.WithSaver(...)` stub, matching its siblings | `internal/client/client_test.go` | – | [I] | 1 | FR-002 |
| S4 | Integration | Verification sweep: build, vet, race test suite, coverage, and a before/after SHA-256 proof that the real credential file is untouched | – | S1, S2, S3 | [W] | 3 | AC-002, AC-001, FR-004 |
| S5 | Feature | Shared test helper `internal/testutil` owning the isolation, the fail-closed guard and the assertion support, so a third package inherits the safety net instead of hand-copying it | `internal/testutil/configdir.go` | S2 | [I] | 1 | FR-005 |
| S6 | Feature | Close the same latent trap in `internal/cmd`: its own TestMain via the shared helper plus a real `config.Save()` round-trip regression test | `internal/cmd/isolation_test.go` | S5 | [I] | 1 | FR-006, AC-001 |

### Step S1 — Package-wide TestMain isolation
- **Covers**: FR-001, AC-001

### Step S2 — Regression tests that fail when isolation is removed
- **Covers**: FR-003, AC-001

### Step S3 — Explicit saver stub on the offending test
- **Covers**: FR-002

### Step S4 — Verification sweep
- **Covers**: AC-002, AC-001, FR-004

**Checkpoint**: `go build ./...` + `go vet ./...` + `go test -race ./...` pass

## Final Verification
- All test suites pass
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- `~/.config/zensu/hosts.json` byte-identical before and after the full test run (size, mtime, SHA-256)
