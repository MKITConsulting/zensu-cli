# Zensu Plan: Print the warnings ghost apply returns

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
Spec (from the starting session, with the route placeholders written as `<id>` and `<scanId>`):

> Make `zensu ghost apply` show the warnings the Zensu server returns from POST /api/products/<id>/ghost/scans/<scanId>/apply.
>
> Verified server contract (merged backend on 2026-11-10, read 2026-11-17): the apply response always carries `warnings`, an array that is empty when nothing degraded; each element is an object {code, repository?, files?, message}. Codes: repository_not_registered, repository_url_invalid, repository_unresolved, repository_removed. Each message ends with "; source files were linked without a repository" (repository_not_registered adds "; an admin can register it under the product's settings"). The originating brief assumed `[]string`; the merged contract uses objects, so the CLI decodes objects.
>
> Code: internal/cmd/ghost.go newGhostApplyCmd. Today the text path decodes only featuresCreated, featuresEnriched, componentsCreated and prints "Applied scan %s: %d features created, %d enriched, %d components created", so every warning is dropped. --json prints the raw response.
>
> Requirements:
> 1. Text mode: after the unchanged summary line on stdout, print one line `warning: <message>` per warning to stderr (cmd.ErrOrStderr()), in server order. This follows the CLI's existing convention (wiki list prints `warning: ...` to stderr).
> 2. Forward compatible: a response without `warnings` (older server) or with an empty array prints exactly what it prints today (summary only, empty stderr). A warning without a message prints nothing.
> 3. --json output stays unchanged: raw response on stdout, nothing added to stderr.
> 4. Table-driven tests in internal/cmd/ghost_test.go in the existing style: warnings present (each printed in order, on stderr only), empty array, field absent, --json with warnings unchanged.
> 5. Update the command's Long help and skills/zensu/reference.md (plus SKILL.md if it describes apply output) to say that warnings are printed. No monorepo file paths or struct names in public text.
> 6. Verify: go test ./internal/cmd/ ./internal/tools/cmdtree/, go vet ./..., GOOS=windows go vet ./..., gofmt -l on touched files.
>
> Then commit (conventional commits, no Claude attribution), push the branch claude/ghost-apply-warnings-6e534d and open a PR against main (routine per user decision 2026-11-10).

Discovery, read on `c0c4482` (main after #53):

- The originating brief came from review finding R1-F8 of the zensu-monorepo pull request that links feature source files to their forge (#827). It described `warnings` as a list of strings; the pull request changed that before it merged on 2026-11-10. On the monorepo's `main` the apply response declares `warnings` as an always-present array of objects with `code`, optional `repository`, optional `files` and `message` (read through the GitHub API on 2026-11-17). The web client shows a translated text per known `code`, with the repository filled in, and falls back to `message` for an unknown code (its ghost scan review page, read on 2026-11-18). The English CLI prints the server's `message` as it is; `--json` keeps `code` for machine readers.
- The scan-create response that `zensu ghost scan` prints carries a different `warnings` field: plain strings about candidate slugs that match existing features. Its text output drops them as well. That is a separate change and stays out of this chain.
- CLI conventions: `zensu wiki list` writes `warning: …` to `cmd.ErrOrStderr()` after its stdout output (`internal/cmd/wiki.go`), and every server-provided string printed to a terminal passes through `sanitizeTerminal` (`internal/cmd/factory.go`), which strips control characters, bidi overrides, invisible format runes and line breaks and turns a tab into a space.
- `docs/commands.json` records no help text, so changing the Long help needs no regeneration; the golden test in `internal/tools/cmdtree` confirms that.
- `skills/zensu/SKILL.md` does not describe what `ghost apply` prints, so only `skills/zensu/reference.md` changes.

Delivery after the review chain: one `feat(ghost):` commit, the branch pushed and a PR against `main`; no tag, no merge.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5 (toolchain go1.26.3), cobra/pflag, stdlib `testing` | **Coverage**: `go test -coverprofile=<scratch>/cover.out ./internal/cmd/ && go tool cover -func=<scratch>/cover.out` (changed files) @ 90% lines (default-90%)

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | When the apply response's `warnings` array holds two objects with messages, `zensu ghost apply s1 --product p1` writes exactly the unchanged summary line to stdout and exactly one `warning: <message>` line per warning, in server order, to stderr | spec |
| AC-002 | When the apply response carries `warnings: []`, or no `warnings` field, stdout is byte-identical to the output of the build before this change (`Applied scan s1: 3 features created, 1 enriched, 2 components created` and a newline) and stderr is empty | spec |
| AC-003 | With `--json`, stdout holds the server's response unchanged, its `warnings` array included, and stderr is empty | spec |
| AC-004 | `zensu ghost apply --help` says that each warning the server returns is printed as a `warning:` line on stderr after the summary and that `--json` keeps the warnings in the response | spec |
| AC-005 | `go test ./internal/cmd/ ./internal/tools/cmdtree/`, `go vet ./...` and `GOOS=windows go vet ./...` pass, and `gofmt -l` lists none of the touched Go files | spec |
| FR-001 | The text path of `ghost apply` decodes `warnings` as objects and writes `warning: ` plus the message to `cmd.ErrOrStderr()`, one line per warning, after the summary line | spec |
| FR-002 | A warning whose message is empty after sanitizing prints nothing; a missing or empty `warnings` array leaves the output as it was | spec |
| FR-003 | Control characters, bidi overrides and line breaks in a server message never reach the terminal: the message passes through `sanitizeTerminal` | derived |
| FR-004 | The Long help of `ghost apply` and `skills/zensu/reference.md` describe the warnings in plain words, without server file paths or type names | spec |
| FR-005 | Table-driven tests in `internal/cmd/ghost_test.go` cover warnings present, an empty array, an absent field, a warning without a message, control characters in a message, and `--json` with warnings | spec |

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
| cli acceptance driver | driver | `go build -o <scratch>/baseline/zensu ./cmd/zensu` on `c0c4482`, run against a loopback fake API with an isolated `ZENSU_CONFIG_DIR` | present | — |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Print apply warnings on stderr | internal/cmd/ghost_test.go | — | [I] | 1 | FR-001, FR-002, FR-003, AC-001, AC-002, AC-003 |
| S2 | Feature | Table-driven apply warning tests | internal/cmd/ghost_test.go | S1 | [I] | 1 | FR-005, AC-001, AC-002, AC-003 |
| S3 | Feature | Help text and skill reference | internal/cmd/skill_docs_test.go | S1 | [I] | 1 | FR-004, AC-004 |
| S4 | Integration | Contract, vet and format checks (verified, no change expected) | internal/tools/cmdtree/main_test.go | S3 | [W] | 1 | AC-005 |

### Step S1 — Print apply warnings on stderr
- **Covers**: FR-001, FR-002, FR-003, AC-001, AC-002, AC-003
- **Files**: `internal/cmd/ghost.go` (`newGhostApplyCmd`, text path only; the `--json` branch and the summary format stay as they are)

### Step S2 — Table-driven apply warning tests
- **Covers**: FR-005, AC-001, AC-002, AC-003
- **Test**: one table-driven test in `internal/cmd/ghost_test.go` on `newFakeAPI`, asserting exact stdout and exact stderr per case and the request count

### Step S3 — Help text and skill reference
- **Covers**: FR-004, AC-004
- **Files**: `internal/cmd/ghost.go` (Long help of `ghost apply`), `skills/zensu/reference.md` (the paragraph under the `ghost` invocations)

### Step S4 — Contract, vet and format checks
- **Covers**: AC-005
- **Files**: none expected; `docs/commands.json` is regenerated only if the golden test reports a difference

**Checkpoint**: `go test ./internal/cmd/ ./internal/tools/cmdtree/` + `go vet ./... && GOOS=windows go vet ./...` + `gofmt -l internal/cmd/ghost.go internal/cmd/ghost_test.go` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
