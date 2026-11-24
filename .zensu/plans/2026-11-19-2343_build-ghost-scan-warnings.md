# Zensu Plan: Print the warnings ghost scan returns

<!--
Authoring rules — this file is written to be COMMITTED. Consuming repos keep
.zensu/plans/ as an audit trail and may later open-source the repository.
  * English only, whatever language the session is conducted in.
  * No absolute paths: name files by their repo-root-relative path. The
    PostToolUse redactor (hooks/post-artifact-redact.sh) rewrites the roots it
    recognizes into <project> / ~ / <home>, but it cannot recognize a customer
    name, an internal hostname, or a credential pasted into the Context section.
-->

## Context
Spec (the follow-up the ghost apply warnings chain named, taken on by the user in this session):

> Make `zensu ghost scan` print the warnings the server returns in the scan-create response (`warnings`: array of strings) as `warning: <message>` lines on stderr after the unchanged summary line, reusing writeWarning; --json output unchanged; absent or empty warnings print exactly as before; table-driven tests, help test, help text and skill docs.

Discovery, read on `bc15472` (main after #54 and #55):

- Server contract: on the monorepo's `main` the scan-create response (`POST /api/products/<id>/ghost/scans`, status 201) carries `scan`, `warnings` and `supersededScanIds`. `warnings` is an always-present array of strings, empty when nothing matched; today the server adds one string per candidate whose slug matches an existing feature of the product, `candidate '<slug>' matches existing feature slug` (read through the GitHub API on 2026-11-19). Unlike the apply response, whose warnings are objects with a `code` and a `message`, these are plain strings.
- `internal/cmd/ghost.go` `newGhostScanCmd`: the text path decodes only `scan.id` and `scan.candidates_total` and prints `Created ghost scan %s (%d candidates)`, so every warning is dropped; `--json` prints the raw response through `printJSON`.
- `writeWarning` in `internal/cmd/erroroutput.go` (added by #54) passes a message through `sanitizeTerminal`, trims it, skips it when nothing is left and writes `warning: <message>` and a newline. `ghost apply` already calls it after its summary line; `ghost scan` reuses it unchanged.
- Tests: `internal/cmd/ghost_test.go` holds the scan tests on `httptest` and the table-driven apply warning test on `newFakeAPI`, the pattern the new scan test follows.
- Help: `TestGhostScanHelp_ShowsCandidateAndComponentSchema` in `internal/cmd/flag_format_test.go` parses the Schema sentence of the scan Long help up to the first `]. ` after `, --components `; a sentence appended after the existing text leaves that parse intact.
- `docs/commands.json` records no help text, so changing the Long help needs no regeneration; the golden test in `internal/tools/cmdtree` confirms that.
- `skills/zensu/SKILL.md` (brownfield section) and `skills/zensu/reference.md` (ghost section) describe only the apply warnings; both gain the scan warnings.

Delivery after the review chain: one `feat(ghost):` commit, the branch `claude/ghost-scan-warnings` pushed and a PR against `main`; no tag, no merge.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5 (go.mod; local toolchain go1.26.3), cobra/pflag, stdlib `testing` | **Coverage**: `go test -coverprofile=<scratch>/cover.out ./internal/cmd/ && go tool cover -func=<scratch>/cover.out` (changed files) @ 90% lines (default-90%)

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | When the scan-create response's `warnings` array holds two strings, `zensu ghost scan --product p1 --candidates '<json-array>'` writes exactly the unchanged summary line to stdout and exactly one `warning: <message>` line per warning, in server order, to stderr | spec |
| AC-002 | When the scan-create response carries `warnings: []`, or no `warnings` field, stdout is byte-identical to the output of the build before this change (`Created ghost scan s9 (2 candidates)` and a newline) and stderr is empty | spec |
| AC-003 | With `--json`, stdout holds the server's response unchanged, its `warnings` and `supersededScanIds` arrays included, and stderr is empty | spec |
| AC-004 | `zensu ghost scan --help` says that each warning the server returns is printed as a `warning:` line on stderr after the summary and that `--json` keeps the warnings in the response | spec |
| AC-005 | `go test ./internal/cmd/ ./internal/tools/cmdtree/`, `go vet ./...` and `GOOS=windows go vet ./...` pass, and `gofmt -l` lists none of the touched Go files | spec |
| FR-001 | The text path of `ghost scan` decodes `warnings` as strings and writes each through `writeWarning` to `cmd.ErrOrStderr()`, one line per warning, after the summary line | spec |
| FR-002 | A warning that is empty after sanitizing prints nothing; a missing or empty `warnings` array leaves the output as it was | spec |
| FR-003 | Control characters, bidi overrides and line breaks in a server warning never reach the terminal | derived |
| FR-004 | The Long help of `ghost scan`, `skills/zensu/reference.md` and the brownfield workflow in `skills/zensu/SKILL.md` describe the scan warnings in plain words, without server file paths or type names | spec |
| FR-005 | Table-driven tests in `internal/cmd/ghost_test.go` cover warnings present, both streams in one buffer, an empty array, an absent field, a blank warning, control characters in a warning, and `--json` with warnings; a help test pins the new `--help` sentences | spec |
| FR-006 | When the summary line or a warning line of `ghost scan` or `ghost apply` cannot be written, the command returns that write error, and no warning follows a summary that could not be written | derived (Phase 6 coverage gap) |
| FR-007 | `ghost scan` and `ghost apply` print a warning the server sends as a plain string or as an object with a `message`; a warning of any other shape prints nothing and never keeps the summary from printing | derived (review finding R1-F2) |
| FR-008 | The `ghost scan` summary line prints the scan id through the terminal sanitizer | derived (review finding R1-F3) |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` (go1.26.3) | present | — |
| gofmt | CLI | `command -v gofmt` | present | — |
| git | CLI | `command -v git` | present | — |
| gh (authenticated) | CLI | `gh auth status` | present | — |
| python3 (loopback fake API for the live CLI check) | CLI | `python3 --version` (3.12.4) | present | — |
| docs/commands.json | fixture | `[ -f docs/commands.json ]` | present | — |
| skills/zensu/reference.md | fixture | `[ -f skills/zensu/reference.md ]` | present | — |
| skills/zensu/SKILL.md | fixture | `[ -f skills/zensu/SKILL.md ]` | present | — |
| cli acceptance driver | driver | `go build -o <scratch>/scan-baseline/zensu ./cmd/zensu` on `bc15472`, run against a loopback fake API with an isolated `ZENSU_CONFIG_DIR` | present | — |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Print scan warnings on stderr | internal/cmd/ghost_test.go | — | [I] | 1 | FR-001, FR-002, FR-003, AC-001, AC-002, AC-003 |
| S2 | Feature | Table-driven scan warning tests | internal/cmd/ghost_test.go | S1 | [I] | 1 | FR-005, AC-001, AC-002, AC-003 |
| S3 | Feature | Help text and skill docs | internal/cmd/flag_format_test.go | S1 | [I] | 1 | FR-004, FR-005, AC-004 |
| S4 | Integration | Contract, vet and format checks (verified, no change expected) | internal/tools/cmdtree/main_test.go | S3 | [W] | 1 | AC-005 |
| S5 | Feature | Write-error returns of scan and apply | internal/cmd/ghost_test.go | S2 | [I] | 1 | FR-006 |
| S6 | Feature | Review round 1 fixes (R1-F1..R1-F4) | internal/cmd/ghost_test.go | S5 | [I] | 1 | FR-004, FR-006, FR-007, FR-008 |

### Step S1 — Print scan warnings on stderr
- **Covers**: FR-001, FR-002, FR-003, AC-001, AC-002, AC-003
- **Files**: `internal/cmd/ghost.go` (`newGhostScanCmd`, text path only; the `--json` branch and the summary format stay as they are)

### Step S2 — Table-driven scan warning tests
- **Covers**: FR-005, AC-001, AC-002, AC-003
- **Test**: one table-driven test in `internal/cmd/ghost_test.go` on `newFakeAPI`, asserting exact stdout and exact stderr per case and the request count

### Step S3 — Help text and skill docs
- **Covers**: FR-004, FR-005, AC-004
- **Files**: `internal/cmd/ghost.go` (Long help of `ghost scan`), `internal/cmd/flag_format_test.go` (help test), `skills/zensu/reference.md` (the ghost section), `skills/zensu/SKILL.md` (the brownfield warning sentence)

### Step S4 — Contract, vet and format checks
- **Covers**: AC-005
- **Files**: none expected; `docs/commands.json` is regenerated only if the golden test reports a difference

### Step S5 — Write-error returns of scan and apply
- **Covers**: FR-006
- **Test**: one table-driven test in `internal/cmd/ghost_test.go` that breaks stdout or stderr with the package's `failingWriter` and asserts the returned error, for both commands; added after the Phase 6 coverage audit measured `internal/cmd/ghost.go` at 89.5%

**Checkpoint**: `go test ./internal/cmd/ ./internal/tools/cmdtree/ ./cmd/zensu/` + `go vet ./... && GOOS=windows go vet ./...` + `gofmt -l internal/cmd/ghost.go internal/cmd/ghost_test.go internal/cmd/flag_format_test.go` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
