# Zensu Plan: Finish the free-text format labels

## Context
Spec (verbatim, as passed to /zensu:build):

> In the zensu CLI repo, finish the free-text format labels on top of branch claude/markdown-flag-help-0f60b0. (1) Check in zensu-monorepo frontend how revision scope summary/details, feature deprecate/split/merge reasons, and ghost candidate rejection reason render (Markdown component vs plain text); append " (Markdown)" or " (plain text)" to the usages of features revision --scope-summary/--scope-details, features deprecate|split|merge --reason, ghost reject --reason, ghost batch --reject-reason; add rows to internal/cmd/flag_format_test.go and markdown/text placeholders to skills/zensu/reference.md; fields without evidence stay unlabelled and are recorded as such. (2) Add a Schema: sentence for candidate and component objects to ghost scan Long help, keys from backend structs ghostCandidateRequest and ghostComponentInput in zensu-monorepo/backend/internal/handlers/ghost.go. (3) Add a test walking NewRootCmd() (visitRunnableCommands helper) that requires every free-text flag on an explicit list to carry a format marker. Done = go vet ./... and go test ./internal/cmd/... pass. English only, no code comments, no commit/push/PR without explicit user approval, no Zensu data changes.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25 module, cobra/pflag CLI, `go test` | **Coverage**: `go test -coverprofile=<scratch>/cover.out ./internal/cmd/...` @ 90% lines (default-90%)

### Base decision
Branch `claude/markdown-flag-help-0f60b0` has not landed: it has no commits beyond `372b06d`, and its reviewed change exists only as uncommitted work in its own worktree. There is nothing to rebase onto, so this chain stacks that uncommitted diff on `f18e36d` (step S0; it applies cleanly with line offsets only) and keeps its own increment separable. Snapshot hashes: tracked-file patch sha256 `b56fdab40fcb208f8fe47b8a9fd47c9bdceb2d057140b8eff671199ad6e3edad`, untracked `internal/cmd/flag_format_test.go` sha256 `ed846cef2b2e5166a5e29ea4b7ed1baeabaa5abf5ca3ec81f40bcacee686d767`.

### Rendering evidence (zensu-monorepo origin/main `7a699200c`)
| Field | Stored as | Rendered by | Verdict |
|-------|-----------|-------------|---------|
| revision scope summary | `feature_revisions.scope_summary` | `frontend/src/pages/feature-detail/RevisionsTab.tsx:96`, plain `<p>` | plain text |
| revision scope details | `feature_revisions.scope_details` | `frontend/src/pages/feature-detail/RevisionsTab.tsx:98-102`, plain `<p>` | plain text |
| deprecate / split / merge reason | lifecycle event `reason` (`backend/internal/handlers/feature_lifecycle.go`, `CreateLifecycleEvent`), served by `backend/internal/handlers/feature_history.go:164-178` | `frontend/src/pages/feature-detail/HistoryTab.tsx:107-117`, i18n interpolation in a plain `<p>` | plain text |
| ghost candidate rejection reason (reject and batch-review) | `ghost_feature_candidates.rejection_reason` (`backend/internal/handlers/ghost.go:895`, `backend/queries/ghost_candidates.sql:84`) | `frontend/src/pages/GhostScanReviewPage.tsx:237-243`, i18n interpolation in a `whitespace-pre-wrap` `<p>` | plain text |

None of these surfaces uses `MarkdownContent` (`frontend/src/components/MarkdownContent.tsx`, react-markdown). React escapes the interpolated text nodes (`frontend/src/i18n/index.ts:41` sets `escapeValue: false` because React escapes). No other non-test frontend source reads these fields. Every one of the six fields therefore has evidence; none stays unlabelled for lack of it.

### Ghost scan schema source (zensu-monorepo origin/main `7a699200c`)
`backend/internal/handlers/ghost.go:332-345` `ghostCandidateRequest`: slug, title, description, componentSlug, confidenceScore, detectedTestFiles, detectedSourceFiles, detectedDocFiles, priority, estimatedEffort, securityClassification, sourceFileAnalyses. `ghost.go:374-378` `ghostComponentInput`: slug, name, description. Enumerations come from the scan validation (`ghost.go:513-573`, `features.go:661-698`): priority critical|high|medium|low, estimatedEffort S|M|L|XL, securityClassification public|internal|confidential|restricted; slug and title are required. confidenceScore is a 0-1 fraction (`frontend/src/lib/status.ts:347-353` shows it as `n * 100` percent). Apply refuses a new feature without componentSlug (`ghost.go:1059-1065`) and creates a missing component, named from its `--components` entry or else the slug (`ghost.go:1131-1144`). The nested `sourceFileAnalyses` shape is elided as `[...]`, as `products bootstrap-apply` does for `subfeatures`: no bundled skill or plugin skill sends it.

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| FR-001 | The working tree carries the sibling branch's uncommitted label change unchanged (hashes above) as the base of this change | spec |
| FR-002 | `features revision --scope-summary` and `--scope-details` usages carry ` (plain text)`; `--scope-summary` keeps its trailing `(required)` | spec |
| FR-003 | `features deprecate`, `features split` and `features merge` `--reason` usages carry ` (plain text)` | spec |
| FR-004 | `ghost reject --reason` and `ghost batch --reject-reason` usages carry ` (plain text)` | spec |
| FR-005 | `internal/cmd/flag_format_test.go` pins each of the seven new usages in its exact-string table | spec |
| FR-006 | `skills/zensu/reference.md` shows `text` placeholders for the seven flags; the `skills/zensu/SKILL.md` ghost batch example shows `"<text>"` | spec |
| FR-007 | `ghost scan` Long help ends with a `Schema:` sentence naming every JSON key of `ghostCandidateRequest` and `ghostComponentInput`, the required keys and the apply rule for componentSlug; a test pins every key | spec |
| FR-008 | A guard test walks `NewRootCmd()` through `visitRunnableCommands` and requires every flag whose name is on an explicit free-text list to carry exactly one of ` (Markdown)` and ` (plain text)`, unless the flag is on an explicit exemption list; it also fails on a list name that matches no flag and on an exemption that no longer applies | spec |
| FR-009 | Free-text flags without rendering evidence stay unlabelled and are recorded (guard exemptions and the final report) | spec |
| AC-001 | `go vet ./...` and `GOOS=windows go vet ./...` exit 0 | spec |
| AC-002 | `go test ./internal/cmd/...` exits 0 | spec |
| AC-003 | `zensu ghost scan --help` prints the Schema sentence with all candidate and component keys | spec |
| AC-004 | `--help` of features revision, deprecate, split, merge and ghost reject, batch prints the ` (plain text)` markers | spec |
| AC-005 | The guard reports a listed free-text flag without a marker, proven on a synthetic command tree | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` (go1.26.3) | present | n/a |
| zensu-monorepo origin/main | fixture | `git -C <zensu-monorepo checkout> rev-parse origin/main` = `7a699200c` | present | n/a |
| Sibling base diff | fixture | sibling worktree diff and test file hashed into the scratchpad | present | n/a |
| CLI acceptance driver | cli | `go run ./cmd/zensu <command> --help` | present | n/a |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S0 | Integration | Stack the sibling's uncommitted label diff | internal/cmd/flag_format_test.go | - | [W] | 1 | FR-001 |
| S1 | Feature | Label the six free-text fields | internal/cmd/flag_format_test.go | S0 | [I] | 1 | FR-002, FR-003, FR-004, FR-005, FR-006, AC-001, AC-002, AC-004 |
| S2 | Feature | Ghost scan schema sentence | internal/cmd/flag_format_test.go | S0 | [I] | 1 | FR-007, AC-001, AC-002, AC-003 |
| S3 | Feature | Free-text format guard test | internal/cmd/flag_format_test.go | S1 | [I] | 1 | FR-008, FR-009, AC-001, AC-002, AC-005 |

### Step S0 — Stack the sibling's uncommitted label diff
- **Covers**: FR-001
- Apply the tracked-file patch and copy the untracked test file; verify both hashes before and the applied result after.

### Step S1 — Label the six free-text fields
- **Covers**: FR-002, FR-003, FR-004, FR-005, FR-006, AC-001, AC-002, AC-004
- Marker placement follows the base: right after the field's noun phrase, before trailing hints such as `(required)` or `(applies to all rejected)`.

### Step S2 — Ghost scan schema sentence
- **Covers**: FR-007, AC-001, AC-002, AC-003

### Step S3 — Free-text format guard test
- **Covers**: FR-008, FR-009, AC-001, AC-002, AC-005
- Explicit list: conditions, description, expected-result, findings, reason, reject-reason, scope-details, scope-summary. Exemptions: the two `work` `--reason` flags, whose contract is short prose for the work channel (`internal/cmd/work.go:1041`) and whose web rendering this chain did not check.

**Checkpoint**: `go test ./internal/cmd/ -run 'FreeText|GhostScanHelp|BootstrapApplyHelp|SkillDocs'` + `go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
