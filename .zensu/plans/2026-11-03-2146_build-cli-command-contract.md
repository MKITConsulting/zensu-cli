# Zensu Plan: CLI command contract (docs/commands.json)

## Context
Feature specification — zensu-cli "CLI command contract" (Phase 2 of zensu-monorepo plan docs/plans/2026-10-18_connect-hub-single-docs-source.md, approved 2026-10-18).

Goal: a committed, machine-readable contract of the zensu CLI command tree that zensu-monorepo vendors (docs/public/vendor/commands.json, pinned by repo/tag/SHA/sha256 in docs/public/sources.json) so a dependency-free scripts/check-public-docs.mjs can resolve every `zensu …` line in backend/internal/services/platformdocs/*.md and frontend/src/config/*Setup.ts / zensuCli.ts against the pinned CLI.

Requirements:
1. New generator internal/tools/cmdtree (package main, runnable with `go run ./internal/tools/cmdtree`), no new module dependencies, builds the tree from cmd.NewRootCmd() (internal/cmd/root.go:68) and writes docs/commands.json.
2. JSON shape optimized for simple, deterministic lookup: full command path -> aliases, positional arguments (accepted counts), subcommands, runnable, hidden/deprecated status, and flags with long name, shorthand, required, takes-a-value, hidden/deprecated. Persistent/global flags must be resolvable on any subcommand. Stable ordering and formatting so regeneration yields byte-identical output; no build-dependent values (version, commit, date).
3. Required-flag semantics and traversal must be shared with internal/cmd/skill_docs_test.go (its requiredFlagRe / visit / lookup helpers) instead of a second implementation.
4. Golden test: fails when the command tree changes and docs/commands.json was not regenerated, with a message naming the regeneration command.
5. Makefile target for regeneration (repo Makefile already has build/test/vet/cover targets).
6. CLI behavior unchanged (no new commands/flags, no help/usage/error text changes).
7. Verify: go test ./internal/tools/... ./internal/cmd/... -timeout 5m, go vet ./... and GOOS=windows go vet ./..., gofmt.
8. Deliver as Conventional Commits (no Claude watermark/co-author), PR in English against main. Do NOT push any tag or merge without explicit maintainer approval. Note: the plan names release v0.11.0, but v0.11.0..v0.20.0 already exist on origin, so the follow-up release version must be re-derived at release time.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go (go.mod `go 1.25.5`), github.com/spf13/cobra v1.10.2, github.com/spf13/pflag v1.0.9, `go test` | **Coverage**: `go test -coverprofile=cover.out ./internal/tools/cmdtree/ ./internal/cmd/ && go tool cover -func=cover.out` @ 90% lines (default-90%)

### Design decisions
- Layout follows the existing `internal/tools/releasesign` tool: `package main`, `main()` delegating to a testable `run(args, stdout)`, stdlib `flag`, tests in the same package.
- The conventions the skill-docs test applies move from `internal/cmd/skill_docs_test.go` into the non-test file `internal/cmd/commandtree.go`, so the test and the generator share one implementation: `VisitCommands` (pre-order walk over a command and its descendants), `IsRequiredFlag` (usage marker `(required)`, `(…, required)` or `(…; required)`, or cobra's `MarkFlagRequired` annotation) and `PlaceholderArg` (a canonical, non-nil UUID that every positional-argument validator in the tree accepts).
- The generator describes the tree the way cobra presents it at execution time: before describing, it adds cobra's runtime defaults (the `help` command on the root, `-h/--help` on every command, `-v/--version` where a version is set), because public docs show lines such as `zensu --version` and `zensu ai-work context --help`.
- Schema 1: top-level `schemaVersion`, `generator`, `root` and `commands`, keyed by full command path (`cobra.Command.CommandPath()`). Per command: `use`, `aliases`, `subcommands`, `runnable`, `hidden`, `deprecated`, `args` (`min`, `max`; `max: null` means unbounded; a command that does not run takes no positional arguments) and `flags`, keyed by long name, each with `shorthand`, `takesValue`, `required`, `persistent`, `hidden` and `deprecated`. Every field is always present.
- Flags are flattened: every command lists every flag it accepts, inherited persistent flags included (`persistent: true`), so a checker resolves a flag with one lookup on the resolved command. `takesValue` is true when the flag consumes the next token as its value (pflag `NoOptDefVal` empty).
- Positional-argument counts are probed with `ValidateArgs` over 0..64 placeholder arguments: acceptance at the probe limit means unbounded; no accepted count, or a non-contiguous set, fails generation and names the command.
- Determinism: maps encode with sorted keys, lists are sorted, 2-space indentation, trailing newline, no HTML escaping, no version, commit or date values.
- Not in schema 1 (additive later if needed): descriptions, flag defaults and types, flag groups such as mutually exclusive flags, conditional requirements.
- Known limitation, reported in the PR: `required` covers only declared requirements, so flags a command enforces only when it runs read `false`: those with the non-canonical marker "(required; …)" (`zensu ai-work start --id`, `zensu ai-work submit --event-id`, `zensu knowledge context-search --query`), conditional ones, and unmarked ones such as `zensu ai-work start --title` and `zensu ai-work submit --expected-version`, `--artifact` and `--criteria-version` (found in review, R1-F4). The shared rule stays deliberately conservative, because `(required; or pass as positional arg)` is conditional; marking these flags is a help-text change and out of scope (CLI behavior unchanged). docs/commands.md states that `false` does not mean optional.
- Resolving a line honors cobra's help short-circuit: `-h`/`--help` skips the argument and required-flag checks (found in review, R1-F5).

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | Running `go run ./internal/tools/cmdtree` from the repository root writes `docs/commands.json`, and a second run leaves `git diff --exit-code docs/commands.json` clean (byte-identical regeneration). | spec |
| AC-002 | The golden test fails when `docs/commands.json` differs from the command tree, and its failure message names the regeneration command; it passes on the committed file. | spec |
| AC-003 | The committed contract lists `version` (shorthand `v`) on `zensu` only, `help` (shorthand `h`) on every command, a value-taking `api-url` on every command (`persistent: true` everywhere except `zensu auth login`, which defines its own local `--api-url`), the alias `kb` on `zensu knowledge`, and `required: true` for `--product` on `zensu meta suggest-workflow`. | spec |
| AC-004 | `go test ./internal/tools/... ./internal/cmd/... -timeout 5m`, `go vet ./...`, `GOOS=windows go vet ./...` and `gofmt -l` over the changed Go files all pass clean. | spec |
| FR-001 | The generator lives in `internal/tools/cmdtree` (package main), uses only the standard library and existing module dependencies, and builds the tree from `cmd.NewRootCmd()`; `go.mod` and `go.sum` stay unchanged. | spec |
| FR-002 | The JSON follows schema 1 as described in Design decisions: commands keyed by full path with use, aliases, subcommands, runnable, hidden, deprecated, args and flags; flags keyed by long name with shorthand, takesValue, required, persistent, hidden and deprecated. | spec |
| FR-003 | Command traversal, the required-flag rule and the positional-argument placeholder have one implementation in `internal/cmd/commandtree.go`, used by both `internal/cmd/skill_docs_test.go` and the generator. | spec |
| FR-004 | Persistent flags are resolvable on every command that inherits them, and cobra's runtime defaults (`help` command, `-h/--help`, `-v/--version`) are part of the contract. | spec |
| FR-005 | Output is deterministic: sorted keys and lists, 2-space indentation, trailing newline, no HTML escaping, no build-dependent values. | spec |
| FR-006 | CLI behavior is unchanged: no command, flag, help, usage or error text changes; production code gains only the unused-by-the-binary helpers in `internal/cmd/commandtree.go`. | spec |
| FR-007 | `make commands` regenerates `docs/commands.json`; `docs/commands.md` documents the schema, its lookup rules and the regeneration, and the README points to it. | spec |
| FR-008 | Generation fails with a non-zero exit and an error naming the command when its positional-argument counts cannot be expressed (no count accepted with the placeholder, or a non-contiguous set). | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` | present | — |
| gofmt | CLI | `command -v gofmt` | present | — |
| GitHub CLI (authenticated) | CLI | `command -v gh && gh auth status` | present | — |
| Acceptance driver `cli` | CLI start command | `go run ./internal/tools/cmdtree` (needs the Go toolchain) | present | — |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Shared command-tree conventions in package cmd, reused by the skill-docs test | internal/cmd/commandtree_test.go | — | [I] | 1 | FR-003, FR-006 |
| S2 | Feature | cmdtree generator: contract model, describe, encode, run | internal/tools/cmdtree/main_test.go | S1 | [I] | 1 | FR-001, FR-002, FR-004, FR-005, FR-008 |
| S3 | Feature | Generator unit tests on a synthetic tree plus the golden test | internal/tools/cmdtree/main_test.go | S2 | [I] | 2 | AC-002, FR-002, FR-004, FR-005, FR-008 |
| S4 | Integration | Generate docs/commands.json, Makefile target, schema docs, README pointers | internal/tools/cmdtree/main_test.go | S2, S3 | [W] | 1 | AC-001, AC-003, AC-004, FR-007 |

### Step S1 — Shared command-tree conventions in package cmd
- **Covers**: FR-003, FR-006
- Add `internal/cmd/commandtree.go` with `VisitCommands`, `IsRequiredFlag` (moves `requiredFlagRe`, adds cobra's required annotation) and `PlaceholderArg` (moves `placeholderArg`).
- Refactor `internal/cmd/skill_docs_test.go` to use them and delete `visitRunnableCommands`, `requiredFlagRe` and `placeholderArg` there; its test results must not change.
- Add `internal/cmd/commandtree_test.go` for the rule's canonical, conditional and annotated cases and the pre-order walk.

### Step S2 — cmdtree generator
- **Covers**: FR-001, FR-002, FR-004, FR-005, FR-008
- `internal/tools/cmdtree/main.go`: `main` → `run(args, stdout)` with `-out` (default `docs/commands.json`); `generate` = `describe(cmd.NewRootCmd())` + `encode`.

### Step S3 — Generator tests
- **Covers**: AC-002, FR-002, FR-004, FR-005, FR-008
- Synthetic tree: aliases, hidden and deprecated commands and flags, usage-marked and annotation-marked required flags, `NoOptDefVal`, inherited persistent flags, help and version defaults, exact, range, unbounded and non-runnable argument counts, both argument-probe failures, determinism and `run` writing the file.
- Golden test: `generate()` equals the committed `docs/commands.json` (CRLF-normalized), failure names `make commands`.

### Step S4 — Integration
- **Covers**: AC-001, AC-003, AC-004, FR-007
- Run the generator, commit `docs/commands.json`, add the `commands` Makefile target, write `docs/commands.md`, point the README to it.

**Checkpoint**: `go test -race ./internal/tools/... ./internal/cmd/... -timeout 5m` over this phase's changed files and the suites that import or invoke them + `go vet ./... && GOOS=windows go vet ./...` and `gofmt -l` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
