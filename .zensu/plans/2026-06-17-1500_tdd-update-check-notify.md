# TDD Plan: Notify-only automatic update-check for zensu CLI

## Context
After a command runs, if a newer GitHub release exists, print a one-line upgrade hint to
stderr. Cheap (no per-invocation network latency), silent in scripts/CI/pipes, opt-out-able,
zero new dependencies. Notify-only — no self-update binary replacement.

New package `internal/update` (cache I/O + HTTP + semver + suppression) + thin wiring in
`cmd/zensu/main.go`. GitHub source: `GET https://api.github.com/repos/MKITConsulting/zensu-cli/releases/latest`,
read `tag_name`. 24h cache (`version-check.json` in `config.ConfigDir()`, atomic 0o600,
mirroring config.go:79-109). Check runs in a goroutine started before `root.Execute()`; a
short grace wait after prints if ready. Compare via hand-rolled `compareSemver` after `v`
normalization (embedded `version.Version` is `1.2.3`, GitHub tag is `v1.2.3`). GitHub requires
a `User-Agent` header (else 403) — set `zensu-cli/<version>`.

**Approach**: Vanilla implementation (TDD discipline disabled via hooks.tddImplementation) | **Tech Stack**: Go 1.25.5, cobra, stdlib `testing` | **Coverage**: `go test -coverprofile=cover.out ./internal/update/... && go tool cover -func=cover.out` @ 90% lines (default-90%)

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| api.github.com/.../releases/latest | endpoint | mocked via `httptest` (base URL is an overridable pkg var); not required for build/test | present | n/a — HTTP-mocked in tests |
| go test -cover | CLI | built into Go toolchain | present | n/a |

## Cross-Layer Value Flow Pairings
(No pairings — `internal/update` is a leaf utility; `main.go` wiring is integration. No new value crosses an unchanged persistence/transport layer.)

## Status Legend
| [ ] Not started | [I] Implemented | [G] GREEN | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts |
|------|------|-------------|-----------|------------|--------|----------|
| S1 | Feature | `internal/update/update.go` — normalize, compareSemver, cache load/save, fetchLatest, suppressed, Notice, Start, Finish | internal/update/update_test.go | – | [G] | 1 |
| S2 | Feature | `internal/update/update_test.go` — table tests: compare/normalize, Notice fresh-vs-stale via httptest + temp config dir, suppression matrix, malformed cache | internal/update/update_test.go | S1 | [G] | 1 |
| S3 | Integration | Wire `update.Start`/`update.Finish` into `cmd/zensu/main.go` around `root.Execute()` | – | S1 | [W] | 1 |
| S4 | Integration | README — auto-check behavior + `ZENSU_NO_UPDATE_CHECK` opt-out note | – | S3 | [W] | 1 |

## Final Verification
- [x] `go test -race ./internal/update/...` pass (full suite `go test ./...` green)
- [x] `go vet ./...` clean + `make build` ok (-> bin/zensu)
- [x] Coverage for `internal/update/update.go` = 88.7% (public API 100%; user accepted — residual is defensive I/O error branches; project sets no threshold)
