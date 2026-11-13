# Zensu Plan: File and stdin input for long free-text flags

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
Spec (verbatim, as passed to /zensu:build):

> Spec (user-approved 2026-11-09): zensu-cli (Go + cobra), add file/stdin input for long free-text flags. For each of these 15 flags add a sibling `--<flag>-file <path|->`: features update --description; products create --description; subfeatures add --description; journeys create --description; roadmap create --description; roadmap update --description; security review --findings and --conditions; wiki create --content; wiki update --content; products vision-create --content; link docs --content (Markdown/text) and ghost scan --candidates and --components; products bootstrap-apply --result (JSON). Behavior: `-` reads stdin (at most one flag per call); exclusive with its inline flag; path must be a regular file; missing/unreadable/non-regular/empty file fails before any API call; text must be UTF-8 without NUL, trailing newlines trimmed; JSON read verbatim through existing validation; caps 256 KiB text, 4 MiB JSON; resolved value is set into the existing flag so Changed semantics, payloads and errors stay unchanged; inline flags unchanged; file-name completion. Tests in internal/cmd. Each new flag documented in --help. Update ground rule 10 in skills/zensu/SKILL.md and skills/zensu/reference.md to point at the new input. Done = go vet ./... and go test ./internal/cmd/... pass. English only, no code comments, Conventional Commits, no Claude attribution, no KEY-N references. No commit/push/PR without explicit user approval; never release.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25 module (toolchain go1.26.3), cobra v1.10.2, pflag v1.0.9, `go test` | **Coverage**: `go test -coverprofile=<scratch>/cover.out ./internal/cmd/...` @ 90% lines (default-90%)

### Base decision
Ground rule 10 and the `(Markdown)` / `(plain text)` labels exist only in commit `f640c92` (`feat(help): name the text format of free-text flags`), a local commit on branch `claude/infallible-mirzakhani-409656` that stacks the earlier uncommitted label change. It is not on origin/main (`4439dd4`). This branch fast-forwarded from `f18e36d` onto `f640c92`, so this chain's commit carries only its own change and is rebased once that commit lands.

### Server limits (Zensu API behavior)
- Every request body is capped at 5 MiB, except binary wiki image uploads.
- Ghost scan candidate and component descriptions over 5000 bytes are rejected.
- The other free-text fields have no per-field length limit.

### Design decisions
- One helper, `withValueFiles`, registers `--<flag>-file` next to an existing string flag and wraps the command's `RunE`. Cobra runs flag-group validation before `RunE`, so `MarkFlagsMutuallyExclusive` rejects a combination with the inline flag; the wrapper then reads every file and stores each value through `Flags().Set`, which marks the inline flag changed. Existing `Changed` checks, payloads and error messages need no edit.
- The usage strings of the 15 inline flags stay unchanged: `internal/cmd/flag_format_test.go` pins them, and pflag lists `--<flag>-file` directly below `--<flag>`. The new usage names the source with pflag's back-quoted varname (`path`), stdin (`-`) and the cap.
- An inline flag marked `(required)` ends in `(required unless --<flag>-file)` instead: either form supplies the value, and its missing-value error names both flags. Decided after the rebase onto the command contract (`docs/commands.json`), whose rule keeps the plain marker for flags a command always rejects when missing; with the plain marker a contract checker would reject a line that passes only the file flag. The contract is regenerated with the new flags. The skill documents both forms as `(--candidates '<json-array>' | --candidates-file <path>)`.
- Errors read `--<flag>-file: <reason>`, naming the path or `stdin`.
- One leading UTF-8 byte order mark is removed for both kinds, so files saved by Windows editors work. Trailing CR and LF are trimmed for text only, matching what `"$(cat "$f")"` produced.
- Stdin is `cmd.InOrStdin()`, so tests inject it with `SetIn`.

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| FR-001 | Each of the 15 flags has a sibling `--<flag>-file` that reads its value from a path, or from stdin when the path is `-`: `features update --description`, `products create --description`, `subfeatures add --description`, `journeys create --description`, `roadmap create --description`, `roadmap update --description`, `security review --findings`, `security review --conditions`, `wiki create --content`, `wiki update --content`, `products vision-create --content`, `link docs --content`, `ghost scan --candidates`, `ghost scan --components`, `products bootstrap-apply --result` | spec |
| FR-002 | A `--<flag>-file` flag and its inline flag cannot be combined in one call | spec |
| FR-003 | At most one `--<flag>-file` flag per call reads stdin | spec |
| FR-004 | A missing, unreadable, non-regular, oversized or empty file fails the command before any API request | spec |
| FR-005 | Text values must be valid UTF-8 without NUL bytes; trailing CR and LF are trimmed and the rest is passed byte for byte | spec |
| FR-006 | JSON values are passed byte for byte to the command's existing JSON validation and payload | spec |
| FR-007 | Text values are capped at 256 KiB and JSON values at 4 MiB | spec |
| FR-008 | The value read from a file is stored in the inline flag, so `Changed` semantics, payloads and existing error messages stay unchanged, and inline flags behave as before | spec |
| FR-009 | Each `--<flag>-file` help line names the path, `-` for stdin and the cap; shell completion completes file names for it | spec |
| FR-010 | `skills/zensu/SKILL.md` ground rule 10 and `skills/zensu/reference.md` point at the new input, and their documented invocations pass the skill-docs tests | spec |
| AC-001 | `go vet ./...` exits 0 | spec |
| AC-002 | `go test ./internal/cmd/...` exits 0 | spec |
| AC-003 | `go run ./cmd/zensu features update --help` and `go run ./cmd/zensu ghost scan --help` list `--description-file path` and `--candidates-file path` with `- for stdin` and their caps | spec |
| AC-004 | `go run ./cmd/zensu features update <id> --description-file <missing path>` exits 1 with an error naming the file, before any network request | spec |
| AC-005 | Under macOS `/bin/bash` 3.2.57, a built `zensu features update <id> --description-file - <<'TOKEN'` sends a description holding backquotes, `$(…)`, `$HOME` and an apostrophe byte for byte to a local recording server | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` (go1.26.3) | present | n/a |
| Base commit | fixture | `git rev-parse HEAD` = `f640c92` | present | n/a |
| CLI acceptance driver | cli | `go run ./cmd/zensu <command> --help` | present | n/a |
| macOS bash 3.2 | CLI | `/bin/bash --version` = 3.2.57 | present | n/a |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Shared value-file helper | internal/cmd/valuefile_test.go | - | [I] | 1 | FR-002, FR-003, FR-004, FR-005, FR-006, FR-007, FR-008, FR-009 |
| S2 | Feature | Wire the 15 flags into their commands | internal/cmd/valuefile_test.go | S1 | [I] | 1 | FR-001, FR-008, FR-009, AC-003, AC-004, AC-005 |
| S3 | Feature | Skill rule 10, reference and doc parser | internal/cmd/skill_docs_test.go | S2 | [I] | 1 | FR-010, AC-001, AC-002 |

### Step S1 — Shared value-file helper
- **Covers**: FR-002, FR-003, FR-004, FR-005, FR-006, FR-007, FR-008, FR-009
- New `internal/cmd/valuefile.go`: `withValueFiles(cmd, ...)` registers each `--<flag>-file`, marks it mutually exclusive with its inline flag and as a file name for completion, and wraps `RunE`; the reader enforces the regular-file, size, UTF-8, NUL and empty rules and the single-stdin rule.

### Step S2 — Wire the 15 flags into their commands
- **Covers**: FR-001, FR-008, FR-009, AC-003, AC-004, AC-005
- Wrap the constructors in `features.go`, `products.go`, `subfeatures.go`, `journeys.go`, `roadmap.go`, `security.go`, `wiki.go`, `links.go` and `ghost.go`; a table-driven test sends each value through a path and through stdin and checks the request body.

### Step S3 — Skill rule 10, reference and doc parser
- **Covers**: FR-010, AC-001, AC-002
- Rule 10 names the `--<flag>-file` flags first and keeps the `"$(cat "$f")"` form for the other free-text flags; reference.md shows the new flags; the skill-docs parser ends an invocation at a heredoc operator, with a notation case.

**Checkpoint**: `go test ./internal/cmd/ -run '<step tests>'` over this phase's changed files and the suites that import or invoke them + `go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
