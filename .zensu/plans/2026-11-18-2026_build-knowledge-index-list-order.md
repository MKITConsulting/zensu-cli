# Zensu Plan: Unambiguous indexed list in `zensu knowledge search --help`

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
Specification (zensu-cli worktree on branch `claude/magical-mirzakhani-6b13c0`, which contains `origin/main` `c0c4482`):

Goal: make the indexed list in `zensu knowledge search --help` unambiguous.

Problem: the `Long` text of `newKnowledgeSearchCmd` (`internal/cmd/knowledge.go:51-52`) says Zensu indexes "(features, visions, journeys, wiki pages, the titles and links of feature docs but not the linked files, mocks and design assets)". Because "mocks and design assets" follows "but not the linked files", it reads as if mocks and design assets were excluded too. They are indexed (own lanes, trust `user_uploaded`; `zensu knowledge sources --help` lists `mock` and `design` among the synced sources; feature ZEN-310 states that uploaded mocks and design assets are indexed into their own lanes).

Changes:
1. `internal/cmd/knowledge.go`: reorder the parenthesized list to "(features, visions, journeys, wiki pages, mocks and design assets, and the titles and links of feature docs but not the linked files)". Keep the rest of the help text unchanged. No nested parentheses.
2. `internal/cmd/knowledge_test.go`, `TestKnowledgeSearchHelp_StatesWhatIsIndexedAndHowItRanks`: replace the pin "titles and links of feature docs but not the linked files" (and the now-subsumed standalone "mocks and design assets" pin) with "mocks and design assets, and the titles and links of feature docs but not the linked files)", and add a guard that fails when the not-the-linked-files clause is followed by another list item (", " after "not the linked files"), with a message saying nothing may follow that clause because it would read as excluded.
3. `README.md` and `skills/zensu/` were checked in Phase 1: they carry no indexed-type list, so they stay unchanged. `docs/commands.json` carries no help text, so it needs no regeneration.

Constraints: help text only — no flag, verb, request body, output or exit code change. No code comments. Conventional commit `fix(knowledge): ...`, no Claude attribution. Public repo: no private monorepo paths, struct names or line numbers in commit or PR text. Never `git stash`.

Delivery after the chain (not a requirement row, because the chain closes before it can be observed): one `fix(knowledge):` commit pushed on a feature branch and a pull request against `main`; no tag, the fix rides along with the next CLI release.

AI-work capture: product Zensu (binding reused), feature ZEN-188 Knowledge Pool Search Commands, confirmed by the user for this goal; run kind work.

Phase 1 findings: outside the `.zensu/` audit artifacts, the old phrase appears only in `internal/cmd/knowledge.go` and `internal/cmd/knowledge_test.go` (`git grep`). The KS-1b plan and log under `.zensu/` quote the old order as history and stay unchanged.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5 (toolchain go1.26.3), cobra, stdlib `testing` | **Coverage**: `go test -coverprofile=<scratchpad>/cover.out ./internal/cmd/ && go tool cover -func=<scratchpad>/cover.out` (changed files) @ 90% lines (default-90%)

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | `zensu knowledge search --help` of a worktree build lists mocks and design assets among the indexed items before the feature-doc clause, and its parenthesized indexed list ends with "but not the linked files)", so no listed item reads as excluded | spec |
| AC-002 | `TestKnowledgeSearchHelp_StatesWhatIsIndexedAndHowItRanks` passes on the final tree and fails when the old order (mocks and design assets after the not-the-linked-files clause) is restored in a throwaway copy | spec |
| AC-003 | `go test ./internal/cmd/` run over the Knowledge, SkillDocs and Readme tests, `go vet ./...`, `GOOS=windows go vet ./...` and `go build ./...` pass on the final tree | spec |
| FR-001 | Help text only: no flag, verb, request body, output table or exit code changes, and the rest of the search help stays word for word | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` | present | — |
| GitHub CLI (pull request after the chain) | CLI | `command -v gh` | present | — |
| Acceptance driver `cli` | CLI | `go build -o <scratchpad>/zensu ./cmd/zensu` then `<scratchpad>/zensu knowledge search --help` | present (Go toolchain present) | — |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Bug Fix | Indexed-list order pin and guard | internal/cmd/knowledge_test.go | — | [I] | 1 | AC-002, AC-003, FR-001 |
| S2 | Bug Fix | Reorder the indexed list in search help | internal/cmd/knowledge_test.go | S1 | [I] | 1 | AC-001, AC-003, FR-001 |

### Step S1 — Indexed-list order pin and guard
- **Covers**: AC-002, AC-003, FR-001
- **IMPL**: in `TestKnowledgeSearchHelp_StatesWhatIsIndexedAndHowItRanks` (`internal/cmd/knowledge_test.go`), replace the "titles and links of feature docs but not the linked files" and "mocks and design assets" pins with "mocks and design assets, and the titles and links of feature docs but not the linked files)", and add a guard that fails when "not the linked files, " appears in the search help. Run the test against the still unchanged help to observe the failure on the old order (bite check).

### Step S2 — Reorder the indexed list in search help
- **Covers**: AC-001, AC-003, FR-001
- **IMPL**: reorder the parenthesized list in the `Long` text of `newKnowledgeSearchCmd` (`internal/cmd/knowledge.go`); no other change.

**Checkpoint**: `go test ./internal/cmd/ -run 'Knowledge|SkillDocs|Readme'` + `go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
- Every `## Manual Acceptance` row carries the user's consent, and its check is carried into the chain-end report's `## Open` for a person to run before release
