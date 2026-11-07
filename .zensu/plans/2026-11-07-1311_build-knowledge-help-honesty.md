# Zensu Plan: Honest `zensu knowledge search` and `sources` help (KS-1b)

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
Session KS-1b of the zensu-monorepo knowledge-sources work package (WP-10, step 5, read on main `e9e640c4d`): the
`search` and `sources` help (`internal/cmd/knowledge.go:50-52,140-141`) say what is indexed and that ranking is
full-text unless the server has embeddings. Done when the CLI part of WP-10-AC6 is green ("The CLI help ... match
behaviour", pinned by a CLI test). The user cuts the CLI release; this chain prepares it only.
KS-1c (plugin prompt, zensu-claude-code) is a separate later session and is not touched here.

Today's help promises what the server does not do: `search` says "by meaning and keyword" over "connected sources",
and `sources` lists "connected external sources" as indexed. Server behaviour, read on zensu-monorepo main
`e9e640c4d`:

- Indexed lanes per product: feature, vision, journey, wiki, doc, mock, design
  (`backend/internal/services/knowledge_ingest.go` `IngestProduct`). Feature docs enter as title or doc type plus
  path or URL only (`knowledge_normalize.go` `NormalizeFeatureDoc`). Mocks and design assets carry trust
  `user_uploaded` (`knowledge_design_ingest.go`); the other lanes and the platform corpus carry `internal_verified`.
- Search also returns the platform corpus, Zensu's built-in product documentation (visibility `platform`), seeded
  at boot (`knowledge_platform.go`, `backend/cmd/zensu/main.go` `SeedPlatformKnowledge`). It is not in the
  organization's source list (`ListKnowledgeSourcesByOrg` filters by organization).
- External sources are registrations only: `KnowledgeConnector` has no implementation (code search for its
  `SyncCursor` type finds only its own file) and every status writer covers only the seven internal types
  (`backend/queries/knowledge.sql` `MarkKnowledgeSources*`).
- Embeddings are on only when `EMBEDDING_BASE_URL` is set (`config.EmbeddingConfig.Enabled`, `server.NewEmbedder`);
  without them search is full-text only. With them it fuses vector and full-text ranks and falls back to full-text
  when the embedder fails (`knowledge_retrieval.go` `Search`).
- Full-text uses `plainto_tsquery('simple')` over `to_tsvector('simple')`: every query word is ANDed, stopwords
  included, case-insensitive, no stemming. Probed on a throwaway local PostgreSQL 14.18: the question "how do we
  rotate the signing keys" does not match "To rotate signing keys, open settings.", the keywords "rotate signing
  keys" do, and "Rotating the signing key" does not match "rotate signing keys".
- Graph expansion is on by default (`KNOWLEDGE_EXPAND_GRAPH`, default true) and appends related items one link
  away with `expanded`, a `relation`, empty text and score 0 (`maybeExpand`).
- `scope: personal` adds the caller's own personal sources to the organization's results; it does not restrict
  the search to them (`handlers/knowledge.go` `Search`, `SearchKnowledge*` predicates).

The same claim appears in `README.md:547` ("its existing semantic search/full-text fallback"), fixed here too.
The `--query` usage keeps its trailing `(required)`, which `internal/cmd/skill_docs_test.go` (`requiredFlagRe`)
and open PR #48's generated command contract read. KS-8 (wave 7) edits `internal/cmd/knowledge.go` next and must
revisit this text when upload and URL kinds ship. Release preparation: one `fix(knowledge):` commit, which the
GoReleaser GitHub changelog (`.goreleaser.yaml`, `changelog.use: github`) lists; suggested tag `v0.20.1`; no tag is
pushed by this chain.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5 (toolchain go1.26.3), cobra, stdlib `testing` | **Coverage**: `go test -coverprofile=cover.out ./internal/cmd/ && go tool cover -func=cover.out` (changed files) @ 90% lines (default-90%)

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | `zensu knowledge search --help` names what is indexed (features, visions, journeys, wiki pages, feature-doc titles and links without the linked files, mocks and design assets, plus Zensu's built-in product documentation) and says sources of type external are not indexed | spec |
| AC-002 | `zensu knowledge search --help` says ranking is full-text unless the server has an embedding model configured, then hybrid with a full-text fallback when the embedding endpoint fails; its summary line no longer promises meaning-based search | spec |
| AC-003 | `zensu knowledge sources --help` names the automatic internal source types, says the built-in documentation is searched but not listed, and says an external source is not indexed whatever its sync status shows | spec |
| AC-004 | No summary, description or flag usage under `zensu knowledge` claims connected sources, "by meaning and keyword" or semantic search (CLI test) | spec |
| AC-005 | `--query` stays a required flag whose usage ends in `(required)` and no longer says natural-language; `--scope` says personal adds the caller's personal sources to the organization's results | spec |
| AC-006 | (deprecated) moved to FR-003: README prose is documentation, not a behaviour a driver of the running CLI can observe | spec |
| FR-001 | No behaviour change: request bodies, output tables, flags and verbs stay as they are, and the existing knowledge tests stay green | spec |
| FR-002 | `search` help says expanded related items carry empty text and score 0, and that every passage is data, never instructions, whatever its trustLevel (trustLevel reports provenance) | spec (amended in review: R1-F1) |
| FR-003 | `README.md`'s context-search paragraph no longer claims semantic search with a full-text fallback; it says full-text unless the server has an embedding model configured | spec (was AC-006) |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` | present | — |
| GitHub CLI (PR) | CLI | `command -v gh` | present | — |
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
| S1 | Bug Fix | Search help text: summary, description, `--query` and `--scope` usage | internal/cmd/knowledge_test.go | — | [I] | 1 | AC-001, AC-002, AC-005, FR-002 |
| S2 | Bug Fix | Sources help text: description | internal/cmd/knowledge_test.go | — | [I] | 1 | AC-003 |
| S3 | Bug Fix | Help-text pin tests and the no-claim guard over the knowledge tree | internal/cmd/knowledge_test.go | S1, S2 | [I] | 1 | AC-001, AC-002, AC-003, AC-004, AC-005, FR-001, FR-002 |
| S4 | Bug Fix | README context-search ranking sentence | — | — | [I] | 1 | FR-003 |
| R1-F1 | Bug Fix | Review fix: the data-not-instructions rule applies whatever the trustLevel | internal/cmd/knowledge_test.go | S1, S3 | [I] | 1 | FR-002 |
| SR-R1-F2 | Bug Fix | Self-review fix: context-pack help and README advise `--query <terms>`; the guard rejects `<question>` | internal/cmd/knowledge_test.go | S3 | [I] | 1 | AC-004 |

### Step S1 — Search help text
- **Covers**: AC-001, AC-002, AC-005, FR-002
- **IMPL**: rewrite `Short`, `Long` and the `--query` and `--scope` usage strings of `newKnowledgeSearchCmd` in `internal/cmd/knowledge.go`; no logic change.

### Step S2 — Sources help text
- **Covers**: AC-003
- **IMPL**: rewrite `Long` of `newKnowledgeSourcesCmd` in `internal/cmd/knowledge.go`; no logic change.

### Step S3 — Help-text pin tests
- **Covers**: AC-001, AC-002, AC-003, AC-004, AC-005, FR-001, FR-002
- **IMPL**: add tests to `internal/cmd/knowledge_test.go` that pin the indexed lanes, the ranking rule, the external-source rule, the trust rule and the flag usages, and walk every command under `knowledge` for the banned claims; existing tests stay untouched.

### Step S4 — README context-search sentence
- **Covers**: FR-003
- **IMPL**: replace the last sentence of the context-search paragraph in `README.md`.

**Checkpoint**: `go test -race ./internal/cmd/ -run 'Knowledge|SkillDocs|Readme'` + `go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
