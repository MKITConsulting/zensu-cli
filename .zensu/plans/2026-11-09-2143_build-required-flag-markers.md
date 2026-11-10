# Zensu Plan: Canonical required markers for flags their command always rejects when missing

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
Follow-up to PR #48 (`docs/commands.json` command contract, merged into `main` as `5715530`). The contract's
`required` field comes from `IsRequiredFlag` (`internal/cmd/commandtree.go`), which reads only cobra's
`MarkFlagRequired` annotation or a usage text ending in `(required)`, `(…, required)` or `(…; required)`. The
maintainer decided the contract PR must not change CLI help text; this chain is that separate, help-text-only change.

Spec (from the starting session):

> Give every CLI flag that a command rejects as missing at run time the canonical required marker in its usage
> text, so `cmd.IsRequiredFlag` and the generated `docs/commands.json` mark it `required: true`. Known flags that
> read `required: false` today although their command rejects them when missing: `zensu ai-work start --id`
> ("(required; keep across retries)") and `--title` (unmarked; `--title is required`); `zensu ai-work submit
> --event-id` ("(required; keep across retries)") and `--expected-version`, `--artifact`, `--criteria-version`
> (unmarked; one combined "are required" error); `zensu knowledge context-search --query` ("(required; sent to the
> configured Zensu backend)"). Steps: (1) find every flag whose absence `RunE` rejects unconditionally; leave
> conditional ones unmarked (e.g. "(required unless --blocking)", "(required; or pass as positional arg)",
> "(required with --blocked)"). (2) Put the canonical marker at the end of each such usage, e.g. "stable work
> UUID, kept across retries (required)". Change no behavior, error text or flag name. (3) Run `make commands`.
> (4) Run `go test ./internal/cmd/... ./internal/tools/... -timeout 5m`, `go vet ./...` and
> `GOOS=windows go vet ./...`; fix any doc line the skill-docs tests then flag in `skills/zensu/SKILL.md` and
> `skills/zensu/reference.md`. (5) Open a PR against `main` (Conventional Commits, English) that says it changes
> `--help` text only. Do not tag or merge.

Discovery (step 1), read on `5715530`: every `fmt.Errorf`/`errors.New` in `internal/cmd` that names a flag, every
"is required"/"are required" error, and every UUID/pattern validator call on a flag value
(`aiWorkUUID`, `requireUUIDFlag`, `requireWorkProduct`, `requireWorkSessionID`, `requireFencing`,
`validContextID`, `captureSessionPattern`) was classified. Two flags beyond the spec's list are rejected
unconditionally without saying "required":

- `zensu ai-work recover --session`: `RunE` first checks `captureSessionPattern.MatchString(session)`, so an
  absent key fails with `--session must be the exact plugin-provided capture key` (`internal/cmd/ai_work_recovery.go`).
- `zensu ai-work bind --confirm`: `RunE` first returns `binding requires explicit human product confirmation via
  --confirm` when the boolean is unset (`internal/cmd/ai_work_binding.go`).

Every other unconditional rejection already carries a canonical marker. Conditional ones stay unmarked:
`subfeatures … --feature` (positional alternative), `work ask --default-option` (unless `--blocking`),
`--reason` with `--blocked`, `--not-before` with `--outcome rate_limited`, `pulse start --head-sha` (without
`--work-order`), `work … --risk-path` (unless `--clear`), at-least-one-of groups and `exactly one of` groups.
`docs/commands.md` cites `(required; keep across retries)` and `--title` of `zensu ai-work start` as examples the
contract cannot see; both stop being true here, so its "Required flags" section is updated too.

Delivery after the review chain: one `fix(cli):` commit and a PR against `main`; no tag, no merge.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5 (toolchain go1.26.3), cobra/pflag, stdlib `testing` | **Coverage**: `go test -coverprofile=cover.out ./internal/cmd/ && go tool cover -func=cover.out` (changed files) @ 90% lines (default-90%)

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | `docs/commands.json` reads `required: true` for `zensu ai-work start` `--id` and `--title`, `zensu ai-work submit` `--event-id`, `--expected-version`, `--artifact` and `--criteria-version`, `zensu ai-work recover --session`, `zensu ai-work bind --confirm` and `zensu knowledge context-search --query`, and no other flag's `required` value differs from `5715530` | spec |
| AC-002 | `zensu ai-work start --help`, `zensu ai-work submit --help`, `zensu ai-work recover --help`, `zensu ai-work bind --help` and `zensu knowledge context-search --help` show each of those flags with a usage ending in `(required)` | spec |
| AC-003 | Run-time behavior is unchanged: each of those commands, run without the flag, fails before contacting a server with the same error text as on `5715530`, and the Go diff changes no flag name, default value or error string | spec |
| AC-004 | `go test ./internal/cmd/... ./internal/tools/... -timeout 5m`, `go vet ./...` and `GOOS=windows go vet ./...` pass, including the skill-docs tests over `skills/zensu/SKILL.md` and `skills/zensu/reference.md` and the committed-contract staleness test | spec |
| FR-001 | Each of the nine usages ends in `(required)`; the three usages that carried `(required; …)` with trailing text are reworded so the marker comes last and their meaning stays | spec |
| FR-002 | `docs/commands.json` is regenerated with `make commands` | spec |
| FR-003 | The "Required flags" section of `docs/commands.md` no longer cites unmarked examples this change marks, and says a flag its command always rejects when missing carries the marker | derived |
| FR-004 | A test in `internal/cmd` ties each of the nine markers to its run-time rejection: the complete invocation passes local validation, the invocation without the flag is rejected before an API client is created, and `IsRequiredFlag` reports the flag required | derived |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` (go1.26.3) | present | — |
| make | CLI | `command -v make` | present | — |
| git | CLI | `command -v git` | present | — |
| gh (authenticated) | CLI | `gh auth status` | present | — |
| docs/commands.json | fixture | `[ -f docs/commands.json ]` | present | — |
| skills/zensu/SKILL.md, skills/zensu/reference.md | fixture | `[ -f skills/zensu/SKILL.md ] && [ -f skills/zensu/reference.md ]` | present | — |
| cli acceptance driver | driver | `go build -o <scratch>/zensu ./cmd/zensu` | present | — |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Mark the nine usages required | internal/cmd/commandtree_test.go | — | [I] | 1 | FR-001, AC-002, AC-003 |
| S2 | Feature | Regression test tying markers to rejections | internal/cmd/commandtree_test.go | S1 | [I] | 1 | FR-004, AC-003 |
| S3 | Feature | Regenerate contract and update its doc | internal/tools/cmdtree/main_test.go | S1 | [I] | 1 | FR-002, FR-003, AC-001 |
| S4 | Integration | Skill-doc conformance and static checks (verified, no change) | internal/cmd/skill_docs_test.go | S3 | [W] | 1 | AC-004 |

### Step S1 — Mark the nine usages required
- **Covers**: FR-001, AC-002, AC-003
- **Files**: `internal/cmd/ai_work.go` (`start --id`, `--title`; `submit --event-id`, `--expected-version`, `--artifact`, `--criteria-version`), `internal/cmd/ai_work_recovery.go` (`--session`), `internal/cmd/ai_work_binding.go` (`--confirm`), `internal/cmd/knowledge_context_search.go` (`--query`)
- **Change**: usage string arguments only; flag names, defaults, `RunE` and error texts untouched

### Step S2 — Regression test tying markers to rejections
- **Covers**: FR-004, AC-003
- **Test**: `TestIsRequiredFlag_MarksEveryFlagItsCommandRejectsWhenMissing` in `internal/cmd/commandtree_test.go`, in a throwaway Git repository (`aiWorkTestRepo`) with a Factory whose `NewClient` returns a sentinel error

### Step S3 — Regenerate contract and update its doc
- **Covers**: FR-002, FR-003, AC-001
- **Files**: `docs/commands.json` via `make commands`; `docs/commands.md` "Required flags"

### Step S4 — Skill-doc conformance and static checks
- **Covers**: AC-004
- **Files**: `skills/zensu/SKILL.md`, `skills/zensu/reference.md` only if the skill-docs tests flag a line (every documented invocation already passes all nine flags)

**Checkpoint**: `go test ./internal/cmd/... ./internal/tools/... -timeout 5m` + `go vet ./... && GOOS=windows go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
