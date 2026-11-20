# Zensu Plan: Progress reports and work status for AI work

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

> Spec (WS-4 "CLI verbs" of the work-status plan; proposed 2026-11-16, taken as approved on 2026-11-20 after the user answered "weiter"): zensu-cli (Go + cobra), add progress reporting and status reading for AI work. Base: origin/main at bc15472 (v0.23.0). Protocol: the work-status contract of the Zensu backend (released as backend v0.120.x): `POST /api/ai-work/capture/units/{id}/progress` takes `{"facts":[...]}` with 1 to 100 facts and at most 256 KiB, stores a batch whole or not at all and answers `{"recorded":n}`; `GET /api/ai-work/capture/units/{id}/status` answers the unit's derived status to its owner; `POST /api/ai-work/capture/units` accepts an optional `taskId`.
>
> SCOPE
> 1. `zensu ai-work start` gains `--task <task-uuid>`, sent as `taskId`. It is refused before any request when the id is invalid and together with `--legacy-metadata` or `--cross-product-reason`.
> 2. `zensu ai-work report [<work-id>] --session <capture-key> --kind <kind> [payload flags] [--timeout 8s]` reports one fact; `--batch` instead of `--kind` reads at most 100 JSON lines and 256 KiB from stdin, each line `{"kind":"...","payload":{...}}`. Payload flags per kind: session_started `--harness` (required) `--client-version` `--branch` `--worktree`; session_ended `--reason`; turn_started, turn_ended, resumed none; segment_started `--phase`; segment_ended `--phase` `--outcome`; step_progress `--done` `--total` `--label`; wait_requested `--subject`; wait_resolved `--subject` `--outcome`; review_round_started `--round`; review_round_ended `--round` `--findings-open` `--findings-fixed` `--findings-blocking` `--finding <severity>:<state>:<title>` (repeatable, at most 20) `--chain-closed` `--limit-reached`; change_request_linked `--repository` `--number` `--state` `--url`; workspace_state `--commits-ahead` `--files-changed` `--uncommitted-files` `--head`; evidence_reported `--check` `--outcome` `--passed` `--failed` `--percent`; blocked `--block-kind` `--reason`; signal `--activity`; work_ended `--reason`. A flag that does not belong to the kind, an unknown kind, an unknown payload field and every value outside the contract's closed lists and bounds are refused locally, before anything is queued or sent. The CLI sends only what the caller passes: the envelope `clientEventId`, `kind`, `occurredAt`, `sessionRef`, `seq`, `payload` and the contract's payload fields. No flag exists for a prompt, an answer, a question text, a command, code, a diff or a file path; the capture key, the host name and local paths are never sent.
> 3. `zensu ai-work status [<work-id>] [--session <capture-key>]` prints the server's JSON.
> 4. Work resolution: without `<work-id>` the unit is the `id` of this worktree's capture receipt `<git-dir>/zensu-ai-work-v1/<capture-key>.json`. `report` then prints `status: skipped` with exit 0 and sends nothing when there is no receipt (`no_active_work`), the receipt is invalid (`invalid_receipt`), it has no confirmed goal scope (`legacy_scope`), capture is paused (`capture_paused`) or it belongs to another API host (`host_mismatch`). With `<work-id>` no receipt is read.
> 5. Queue, owned by `report`: one state file per unit under `<git-dir>/zensu-ai-work-v1/progress/`, written atomically, guarded by operating-system file locks. `sessionRef` is the first 24 hex characters of a SHA-256 over a fixed prefix and the capture key. `clientEventId` is `<sessionRef>:<kind>:<seq>`; `seq` is the session's counter in the state file, the larger of the last value plus one and the seconds since 2025-01-01T00:00:00Z. Every fact except `signal` is sealed (id, seq, occurredAt, payload) when it is queued. A `signal` goes into one open slot per session and takes its id and time only when it enters a batch: at most one per session in 60 seconds, a later signal inside the minute replaces the slot's content, another fact of the session discards an unsent slot, a slot older than 60 seconds is never sent. A batch is stored as in flight before the request and is resent byte for byte until the server answers. A second lock per unit allows one request at a time, so facts arrive in queue order; a call that cannot get it in time answers `queued` with `busy`. Bounds: at most 500 facts per unit (the oldest queued go first), no fact older than 24 hours, the whole call bounded by `--timeout`, state of units idle for 30 days removed.
> 6. Results of `report` on stdout as JSON with exit 0: `sent` (queue empty, `recorded` counted), `queued` (`reason` one of `network`, `timeout`, `server_error`, `rate_limited` with `retryAfterSeconds`, `backoff`, `busy`), `merged` (a signal absorbed into the open minute), `unsupported`, `skipped`. `dropped` lists facts the server refused for good in this call with `kind`, `code` and `field`. Exit 1: invalid input, no personal login, no Git worktree, a failed sign-in refresh, a refusal of the whole unit.
> 7. Unsupported: a 404 or 405 on the progress route is confirmed with `GET /api/ai-work/capture/units/{id}`. 200 means the route is missing: `report` prints `status: unsupported`, exits 0, keeps nothing and remembers the verdict for 10 minutes per unit, during which calls send nothing. 404 means the work is not available: exit 1 with the server's error and the unit's queue discarded. Anything else is a transient failure. `status` applies the same check, additionally requires the unit to be the caller's own (`ownerUserId` equals `reportingUserId`), and fails with exit 1 and the error code `unsupported`.
> 8. Server refusals: `details.index` with `invalid_progress_fact`, `unknown_progress_fact_kind`, `unknown_progress_fact_field` or `ai_work_progress_event_conflict` removes that fact, records it in a bounded log of 20 in the state file and sends the rest in the same call; `request_too_large` and `progress_batch_too_large` split the batch; a refusal of the batch's form without an index is bisected until the refused fact stands alone; 403, a confirmed 404 and `ai_work_progress_unit_full` discard the unit's queue and exit 1, and after `unit_full` later calls send nothing; network errors, timeouts, 5xx, 429 and any other 409 keep the batch, with a back-off of 5 to 60 seconds and `Retry-After` honoured.
> 9. Vocabulary: `internal/cmd/testdata/work-status-vocabulary.json` holds a copy of the `vocabulary` section of the contract fixture with its source commit. One test compares every closed list of the CLI with it; a second compares the copy with the original when `ZENSU_WORK_STATUS_CASES` points at it and reports a skip otherwise.
> 10. Docs: `skills/zensu/reference.md`, `skills/zensu/SKILL.md` and `README.md` describe `report`, `status` and `--task`; `docs/commands.json` is regenerated.
>
> OUT OF SCOPE: a release, a tag, plugin or backend changes, status derivation in the CLI, a verb classification (none exists in zensu-cli yet).
>
> ACCEPTANCE CRITERIA: AC-001 to AC-021 as listed in the Requirements table below, in the spec's wording.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25 module (toolchain go1.26.3), cobra v1.10.2, pflag v1.0.9, golang.org/x/sys, `go test` | **Coverage**: `go test -coverprofile=<scratch>/cover.out ./internal/cmd/... ./internal/config/...` @ 90% lines (default-90%)

### Facts verified before planning
- The branch was fast-forwarded to `bc15472` (tag `v0.23.0`). No verb classification exists in the source or in `docs/commands.json`.
- The backend's contract document, its shared fixture and its capture handlers were read on the backend's main branch at `48633682a`; the fixture was last changed by the commit that introduced it (`81aecb985`). Backend releases `v0.120.0` and `v0.120.1` contain that commit.
- Server behaviour the design relies on, read in the backend's source: a batch is decoded and validated before the unit is looked up; an unknown unit answers 404 with the code `not_found`, another member's login answers 403 on the progress route and 404 on the status read; a refusal of one fact carries `details.index` and `details.field`; `Retry-After` is set from the limit's wait; a replay compares `kind`, `sessionRef`, `seq`, `occurredAt` (to the microsecond) and the normalized payload; no handler is registered for unknown routes, so the router answers them with a plain 404.
- The capture detail read `GET /api/ai-work/capture/units/{id}` answers participants and administrators and carries `ownerUserId` and `reportingUserId`.

### Design decisions
- **Files.** `ai_work_progress_facts.go` holds the closed lists, the payload types and their checks; `ai_work_progress_queue.go` the state file, the locks, sealing and bounds; `ai_work_progress_send.go` the flush loop and the outcome rules; `ai_work_report.go` and `ai_work_status.go` the two commands. `ai_work.go` only gains `--task` and the two registrations.
- **Payloads are typed.** Each kind has a struct with the contract's field names. A batch line is decoded into it with unknown fields disallowed, and flags fill it; the stored payload is marshalled from the struct, so only allow-listed fields can leave the process.
- **Locks.** The stored login already had a cross-platform advisory file lock and a Windows-safe replace in `internal/config`. Review round 1 moved both to the leaf package `internal/safefile` (`Lock`, `ErrLockBusy`, `Replace`), which the stored login and the queue use, instead of a second implementation or general helpers exported from the login package. A lock file is separate from the state file, because the state file is replaced by rename.
- **Two locks per unit.** A short state lock guards every read-modify-write of the state file and is never held across a request. A send lock is held for the whole flush, so one request is in flight per unit and facts arrive in queue order. A caller always stores its facts under the state lock first, so a busy send lock never loses a fact.
- **Time.** One package-level clock and one package-level random-free id rule make every id reproducible in tests. `occurredAt` is written in UTC with microsecond precision, the precision the server compares.
- **The verdict "unsupported" is proven, not inferred from a body.** A proxy can rewrite a 404 body, so the existing detail read decides: a readable unit and a 404 on the new route can only mean the route is missing.
- **A stale fact is not delivered late.** The server orders facts by arrival and stamps them with its own clock, so facts kept for an unsupported server are dropped, and a signal slot is only sealed while it is fresh.
- **`status` reports "unsupported" as an error.** The server's answer has a `status` object of its own, so a typed stdout result would collide with it. The error carries the code `unsupported` in the JSON error line.
- **AC-021 is deprecated in this plan.** It needs the production host and the personal login, which the acceptance driver of this workflow does not use. It was not run in this chain: a call against the production host uses the user's personal login and is made only with the user's explicit approval, so the final report offers it as a decision.

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| FR-001 | `ai-work start` accepts `--task <task-uuid>` and sends it as `taskId`; it is refused before any request when the id is invalid and together with `--legacy-metadata` or `--cross-product-reason` | spec |
| FR-002 | `ai-work report` reports one fact from `--kind` and the kind's payload flags, or up to 100 facts from JSON lines on stdin with `--batch`; everything outside the contract's kinds, fields, closed lists and bounds is refused locally | spec |
| FR-003 | `ai-work status [<work-id>] [--session <capture-key>]` prints the server's JSON | spec |
| FR-004 | Without `<work-id>` the unit comes from the capture receipt of `--session`; `report` answers `skipped` with a reason when the receipt is missing, invalid, legacy, paused or bound to another host | spec |
| FR-005 | A queue per unit under `<git-dir>/zensu-ai-work-v1/progress/` assigns `sessionRef`, `seq` and `clientEventId`, seals facts, merges `signal` facts, stores a batch before sending it, serializes requests per unit and holds the stated bounds | spec |
| FR-006 | `report` prints one of `sent`, `queued`, `merged`, `unsupported`, `skipped` as JSON with exit 0, lists refused facts under `dropped`, and exits 1 only for invalid input, a missing personal login, a missing Git worktree, a failed sign-in refresh and a refusal of the whole unit | spec |
| FR-007 | A 404 or 405 on the new route is confirmed with the capture detail read: readable means unsupported, 404 means not available, anything else is transient; the verdict is remembered for 10 minutes per unit | spec |
| FR-008 | Refusals of one fact remove that fact and send the rest; size refusals split the batch; an unindexed refusal is bisected; refusals of the whole unit discard its queue; transient answers keep the batch with back-off and `Retry-After` | spec |
| FR-009 | A copy of the contract fixture's `vocabulary` section lives in `internal/cmd/testdata/`, checked against the CLI's lists and, when the original is at hand, against the original | spec |
| FR-010 | `skills/zensu/reference.md`, `skills/zensu/SKILL.md` and `README.md` describe `report`, `status` and `--task`, and `docs/commands.json` is regenerated | spec |
| AC-001 | `go vet ./...` and `GOOS=windows go vet ./...` exit 0 and `go build ./...` succeeds | spec |
| AC-002 | `go test ./internal/cmd/... ./internal/config/... ./internal/tools/...` exits 0 | spec |
| AC-003 | `start --task <uuid>` sends `taskId`; an invalid id, `--legacy-metadata` or `--cross-product-reason` with `--task` fail before any request | spec |
| AC-004 | `report` with flags posts `{"facts":[...]}` to the progress route with the personal login and prints `status: sent`; an API key, a session token and a missing login are refused without a request | spec |
| AC-005 | For each of the 18 kinds the flags produce exactly the contract's payload fields, and the envelope holds exactly `clientEventId`, `kind`, `occurredAt`, `sessionRef`, `seq` and `payload` | spec |
| AC-006 | Local validation refuses with exit 1, no request and nothing queued: an unknown kind, a flag of another kind, an unknown value of a closed list, prose with a line break, a tab, a control character or more than 120 characters, a `worktree` with a path separator, a `url` that is not https or carries a query, a fragment or credentials, an invalid `branch`, `done` above `total`, `total` above 500, `round` above 50, more than 20 findings, `blocking` above `open`, `closed_by_person`, and a wait outcome that does not belong to its subject | spec |
| AC-007 | `--batch` sends N valid lines as N facts in input order; an unknown key, an unknown payload field, an invalid line, more than 100 lines or more than 256 KiB refuse the whole call before any request | spec |
| AC-008 | With a fixed clock and capture key the ids are exactly `<sessionRef>:<kind>:<seq>`, `seq` rises strictly across calls, and the capture key appears in no request | spec |
| AC-009 | A failed send (network error or 5xx) exits 0 with `status: queued`; the next call first sends the stored batch with a byte-identical body and then the new fact | spec |
| AC-010 | A 429 with `Retry-After` answers `queued`, `rate_limited` and `retryAfterSeconds`; no request goes out before that time and the facts are kept | spec |
| AC-011 | A 404 on the progress route with a readable detail answers `status: unsupported` with exit 0 and keeps nothing; for 10 minutes further calls send no request; after that one call probes again | spec |
| AC-012 | A 404 whose detail read answers 404 exits 1 with the server's error and discards the unit's queue; so do a 403 and `ai_work_progress_unit_full`, and after `unit_full` later calls send nothing | spec |
| AC-013 | A refusal with `details.index` removes exactly that fact, lists it under `dropped` with code and field, and the remaining facts are recorded in the same call; `request_too_large` splits the batch; a refusal without an index is bisected | spec |
| AC-014 | At most one `signal` per session is sent in 60 seconds; a signal inside the minute answers `merged` and sends nothing; the next one after the minute carries the latest activity; signals during an outage arrive as one; another fact of the session discards an unsent signal; no signal is ever sent twice with different contents | spec |
| AC-015 | Parallel `report` calls for one unit yield distinct ids, the test server never sees two overlapping requests for that unit, and every fact arrives exactly once in `seq` order | spec |
| AC-016 | Without `<work-id>` the receipt of `--session` names the unit; a missing receipt, paused capture, a legacy scope, an invalid receipt and another API host answer `skipped` with their reason, exit 0 and no request | spec |
| AC-017 | `status <work-id>` prints the server's JSON and `status --session` resolves the receipt; a 404 with a readable detail of the caller's own unit exits 1 with the code `unsupported`; a 404 whose detail read answers 404 exits 1 as not available | spec |
| AC-018 | The queue holds at most 500 facts and no fact older than 24 hours, and a call against a server that never answers returns within `--timeout` with `queued` | spec |
| AC-019 | Every closed list of the CLI equals the vocabulary copy, and the drift test passes against the contract fixture of the backend's main branch | spec |
| AC-020 | `skills/zensu/reference.md`, `skills/zensu/SKILL.md` and `README.md` describe `report`, `status` and `--task`; the skill-docs tests pass and `docs/commands.json` matches the command tree | spec |
| AC-021 | (deprecated) against the released backend, `status` and `report` for a work id that does not exist answer the server's `not_found`, which also shows that a batch holding every fact kind passes the server's strict decoding; dropped as a gate because it needs the production host and the personal login, and run once as a live check outside the gate | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` (go1.26.3) | present | — |
| gofmt | CLI | `command -v gofmt` | present | — |
| git | CLI | `command -v git` | present | — |
| python3 (loopback fake API for the live CLI checks) | CLI | `command -v python3` | present | — |
| docs/commands.json | fixture | `[ -f docs/commands.json ]` | present | — |
| skills/zensu/reference.md | fixture | `[ -f skills/zensu/reference.md ]` | present | — |
| contract fixture of the backend's main branch | fixture | `[ -f <scratch>/mono/work-status-cases.json ]` (exported from the backend repository's main branch, sha256 `42f22517…`) | present | — |
| cli acceptance driver | driver | `go build -o <scratch>/bin/zensu ./cmd/zensu`, run against a loopback fake API with an isolated `ZENSU_CONFIG_DIR` | present | — |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Shared file lock and atomic replace | internal/safefile/lock_test.go | — | [I] | 1 | FR-005 |
| S2 | Feature | Progress fact model and local validation | internal/cmd/ai_work_progress_facts_test.go | — | [I] | 1 | FR-002, AC-005, AC-006, AC-007 |
| S3 | Feature | Vocabulary copy and its two tests | internal/cmd/ai_work_progress_vocabulary_test.go | S2 | [I] | 1 | FR-009, AC-019 |
| S4 | Feature | Queue state, ids, signal slot and bounds | internal/cmd/ai_work_progress_queue_test.go | S1, S2 | [I] | 1 | FR-005, AC-008, AC-014, AC-018 |
| S5 | Feature | Flush loop, outcome rules and unsupported check | internal/cmd/ai_work_progress_send_test.go | S4 | [I] | 1 | FR-007, FR-008, AC-009, AC-010, AC-011, AC-012, AC-013, AC-015, AC-018 |
| S6 | Feature | `ai-work report` command | internal/cmd/ai_work_report_test.go | S5 | [I] | 1 | FR-002, FR-004, FR-006, AC-004, AC-007, AC-016 |
| S7 | Feature | `ai-work status` command and coded error | internal/cmd/ai_work_status_test.go | S5 | [I] | 1 | FR-003, FR-007, AC-017 |
| S8 | Feature | `--task` on `ai-work start` | internal/cmd/ai_work_test.go | — | [I] | 1 | FR-001, AC-003 |
| S9 | Integration | Skill reference, skill map, README and command contract | internal/cmd/skill_docs_test.go | S6, S7, S8 | [W] | 1 | FR-010, AC-001, AC-002, AC-020 |

### Step S1 — Shared file lock and atomic replace
- **Covers**: FR-005
- **Files**: `internal/safefile/lock.go` (`Lock(ctx, path)`, `ErrLockBusy`), `internal/safefile/replace.go` (`Replace`), the platform files `lock_unix.go`, `lock_windows.go`, `replace_other.go`, `replace_windows.go` and `replace_internal_test.go` moved from `internal/config` with only their package clause changed, `internal/safefile/lock_test.go`, `internal/safefile/lock_internal_test.go`, `internal/config/lock.go` and `internal/config/config.go` (the stored login uses the package), `internal/config/lock_test.go`

### Step S2 — Progress fact model and local validation
- **Covers**: FR-002, AC-005, AC-006, AC-007
- **Files**: `internal/cmd/ai_work_progress_facts.go` (closed lists, one payload type per kind, checks for prose, branch, worktree, version, repository, link and head, strict decoding of a batch line), `internal/cmd/ai_work_progress_facts_test.go`

### Step S3 — Vocabulary copy and its two tests
- **Covers**: FR-009, AC-019
- **Files**: `internal/cmd/testdata/work-status-vocabulary.json`, `internal/cmd/ai_work_progress_vocabulary_test.go`

### Step S4 — Queue state, ids, signal slot and bounds
- **Covers**: FR-005, AC-008, AC-014, AC-018
- **Files**: `internal/cmd/ai_work_progress_queue.go` (state file, directory checks, state and send lock, `sessionRef`, `seq`, sealing, signal slot, age and size bounds, removal of idle state), `internal/cmd/ai_work_progress_queue_test.go`

### Step S5 — Flush loop, outcome rules and unsupported check
- **Covers**: FR-007, FR-008, AC-009, AC-010, AC-011, AC-012, AC-013, AC-015, AC-018
- **Files**: `internal/cmd/ai_work_progress_send.go` (batch forming, in-flight batch, request, classification of answers, detail probe, split and bisect, back-off), `internal/cmd/fakeapi_test.go` (reply headers; the tests needed no custom handler hook), `internal/cmd/erroroutput.go` (a local error with a code in the JSON error line, moved here from S7 because the refusal of a full unit needs it), `internal/cmd/ai_work_progress_send_test.go`

### Step S6 — `ai-work report` command
- **Covers**: FR-002, FR-004, FR-006, AC-004, AC-007, AC-016
- **Files**: `internal/cmd/ai_work_report.go` (flags, kind and flag matching, batch input, receipt resolution, result JSON), `internal/cmd/ai_work.go` (registration), `internal/cmd/ai_work_report_test.go`

### Step S7 — `ai-work status` command and coded error
- **Covers**: FR-003, FR-007, AC-017
- **Files**: `internal/cmd/ai_work_status.go`, `internal/cmd/ai_work.go` (registration), `internal/cmd/ai_work_status_test.go`, `internal/cmd/erroroutput_test.go` (the JSON error line of the coded local error that S5 added)

### Step S8 — `--task` on `ai-work start`
- **Covers**: FR-001, AC-003
- **Files**: `internal/cmd/ai_work.go`, `internal/cmd/ai_work_feature_scope.go`, `internal/cmd/ai_work_test.go`

### Step S9 — Skill reference, skill map, README and command contract
- **Covers**: FR-010, AC-001, AC-002, AC-020
- **Files**: `skills/zensu/reference.md`, `skills/zensu/SKILL.md`, `README.md`, `docs/commands.json` (regenerated with `go run ./internal/tools/cmdtree`), `internal/cmd/commandtree_test.go` (the required `--session` of `report` and the optional work id), `internal/cmd/ai_work_progress_vocabulary_test.go` (two tests that pin the reference's kind list and enum rows to the command's lists), `internal/cmd/ai_work_report.go` (the exit-code sentence of the help text)

### Review round 1
The first review returned 17 findings (3 important, 14 suggestions); all were fixed in this round.
- **Send rules** (`internal/cmd/ai_work_progress_send.go`): the gate (full unit, unsupported verdict, running wait) is checked again after the wait for the send lock, and a call whose queued facts a parallel call discarded fails instead of answering `sent`; a receipt that breaks off after a success status is retried; an answer without one of the API's error codes never drops a fact or refuses the work (a body too large is still split while the batch holds more than one fact); a detail read that answers 404 without a code decides nothing; `Retry-After` is clamped before it becomes a duration; the refused `field` is kept only when it is a field path of at most 64 characters.
- **Queue** (`internal/cmd/ai_work_progress_queue.go`): a stored wait longer than an hour is dropped; a lock path that is not a regular file is refused; a session entry without a record is removed on load; the state counts its discards; a call that delivered its own facts also sends what another work of the same worktree and API host left behind.
- **Shared names**: `aiWorkCaptureDirName`, `aiWorkUnitsPath` and `aiWorkUnitPath` live in `internal/cmd/ai_work.go` and replace the older literals in `ai_work_binding.go`, `ai_work_recovery.go` and the `get` and `submit` commands; the group's help names progress facts and status reads.
- **Tests**: `internal/cmd/ai_work_progress_send_test.go`, `internal/cmd/ai_work_progress_queue_test.go`, `internal/safefile/*_test.go`, `internal/cmd/flag_format_test.go` (`--finding` and `--label` are free-text flags).
- **Docs**: `skills/zensu/reference.md`, `skills/zensu/SKILL.md`, `README.md` and the help of `ai-work report` (optional counts, `workId`, the CLI's own error codes, what the queue keeps and for which work). `docs/commands.json` holds no help text and stayed as round 0 left it.

### Review round 2
The re-review of round 1 returned one important finding (R2-F1), fixed in this round: the time budget of `flush` was checked before the gate, so a call whose budget had ended skipped the gate and could answer `sent` for a batch that had vanished from the queue. The budget is now checked inside the batch-forming step, after the gate and the discard verdict, so a call without a budget still reads the state once and answers with its current count (`internal/cmd/ai_work_progress_send.go`, two tests in `internal/cmd/ai_work_progress_send_test.go`).

### Self-review
The re-review of round 2 left two suggestions, R3-F1 and R3-F2, and the review loop closed at its limit of two fix rounds. The terminal self-review fixed both in its one fix round, without another independent review. R3-F1: a call that never gets the send lock before its budget ends no longer answers from the count it took before the wait; it reads the state once (`settle`), fails when a parallel call discarded the queue, answers `unsupported` or the unit-full error when a parallel call learned that, and otherwise answers with the current count. The gate is now one method, `closed`, used by the first update, the send loop and that exit. R3-F2: the tests pin the order of the gate (verdicts, then the budget, then a stored wait) through the stored state, for the send loop and for the busy exit; each was run once against a variant of the code without the check and failed there.

**Checkpoint**: `go test ./internal/cmd/ ./internal/config/ ./internal/safefile/ ./internal/tools/cmdtree/` over this phase's changed files + `go vet ./... && GOOS=windows go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
