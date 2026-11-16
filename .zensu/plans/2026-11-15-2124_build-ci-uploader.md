# Zensu Plan: Customer-usable CI uploader (`zensu ci` and a thin public coverage action)

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
Spec (verbatim, as passed to /zensu:build; absolute paths shortened):

> Specification: implement PR A of the approved plan ~/.claude/plans/mellow-stirring-steele.md in the zensu-cli worktree <project> — sections "PR A — zensu-cli" (A1 login accepts a key without read scope; A2 --api-key-stdin, no credentials on disk, CLI-wide key redaction in apiError; A3 `zensu ci coverage|commits|pull-request` command group with --github parity, annotation encoding, response-shape checks, --end-of-options; A4 thin composite action actions/coverage/action.yml installing a signature-verified CLI release via install.sh with ed25519 verification (OpenSSL 3 pkeyutl -verify -rawin, ZENSU_REQUIRE_SIGNATURE, ZENSU_RELEASE_URL, embedded keys kept equal to internal/update/keys.go by a Go test), release.yml guard that the action version default equals the tag; A5 tests; A6 docs), the zensu-cli lines of "Verification" (go vet ./..., GOOS=windows go vet ./..., go test -race ./internal/... -run 'CI|Login|ValidateAPIKey|Action|SkillDocs|Readme|Redact', go test -race ./... once, sh -n install.sh), and the A4 Trust bullet as updated 2026-11-03. Also generate the new `ci` group into docs/commands.json (#48), honour required-flag marking (#50), text-format names for free-text flags (#51), reuse the file/stdin reading from #52 where it overlaps --body-file. Action version default = v0.22.0 (next release; re-check tags before the PR). Done = PR open with green CI, verification commands run and reported, review chain closed.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25 module (toolchain go1.26.3), cobra v1.10.2, pflag v1.0.9, go.yaml.in/yaml/v3, POSIX sh (install.sh), GitHub Actions composite action, `go test` | **Coverage**: `go test -coverprofile=<scratch>/cover.out ./internal/cmd/... ./internal/auth/...` @ 90% lines (default-90%)

### Source plan (PR A of the approved plan, condensed)
The approved plan was written in a monorepo session (decision of 2026-10-21, red-team review of 2026-10-26, trust update of 2026-11-03). The public `MKITConsulting/zensu-cli` gets typed `zensu ci` commands that hold the only implementation of the CI upload; a thin composite action in the same repository installs the CLI release of its own version and calls them. Action tag = CLI version.

- **A1** `internal/auth/oauth.go` `ValidateAPIKey`: `403` with code `forbidden` means the server authenticated the key but it lacks `read`; accept it as a restricted user key. Other 403 codes stay rejected; `agent_key_not_allowed` stays the agent kind. `internal/cmd/login.go` says that only commands within the key's scopes work (for example `zensu ci`). Background: the web app's "CI pipeline" key profile grants only scope `ci`, and the backend answers `GET /api/products` for such a key with `403 {"code":"forbidden","message":"insufficient scope"}` (backend `middleware/scope.go`).
- **A2** Every `zensu ci` command accepts `--api-key-stdin`: it reads the key from stdin, builds the client from it alone, and neither reads nor writes the config. It requires an explicit API URL (`--api-url` or `ZENSU_API_URL`), so a self-hosted key never goes to the built-in default host. No environment variable carries the key. Without the flag the commands use the normal login. CLI-wide: response text echoed in an error never contains a credential; the client redacts its API key and tokens in `apiError` text, plain text and raw body.
- **A3** `zensu ci` group (`internal/cmd/ci*.go`, registered in `root.go`), reusing `Factory.request`, `readResponse`/`apiError`, `printJSON`; `--product <id>` documented as required; human output by default plus `--json`. `--github` (explicit opt-in) reads the GitHub Actions variables; explicit flags win; warnings and errors become `::warning::`/`::error::` annotations with `%`, CR and LF encoded and server text sanitized; without `--github` the CLI prints no `::` lines.
  - `ci coverage --product <id> --file <report> [--format json-summary] [--root <dir>] [--ci-run-url <url>] [--markdown-summary <file>] [--github] [--api-key-stdin] [--json]`: Istanbul json-summary, each non-`total` entry → `{path, linesCovered, linesTotal}` plus `branchesCovered/branchesTotal` when `branches.total > 0`; path relative to `--root` (default git top-level, else the working directory), forward slashes, outside paths unchanged; 0 files → warning, no request, exit 0; over 10,000 files → error; `POST /api/ci/coverage {productId, source:"ci", files, ciRunUrl?}`; prints recorded features, unmapped count, a warning when nothing mapped; the Markdown summary is the table the monorepo action writes today. Under `--github`: root = `GITHUB_WORKSPACE`, run URL = `GITHUB_SERVER_URL/GITHUB_REPOSITORY/actions/runs/GITHUB_RUN_ID`, summary = `GITHUB_STEP_SUMMARY`.
  - `ci commits --product <id> [--range <rev-range>] ...`: under `--github` exact bash parity with the monorepo `actions/coverage/action.yml` commits step (head = `GITHUB_SHA`, for `pull_request` the event's `head.sha`; base = `pull_request.base.sha` or push `before`; only `push` and `pull_request` get a base; a head missing from the checkout falls back to `HEAD`; an all-zero base is dropped; a base missing from the checkout → warning to use `fetch-depth: 0`, head only); without flags `HEAD`. `git log -z --format=%H%x1f%an%x1f%aI%x1f%B --end-of-options <range>`; messages right-trimmed; keeps messages with a `[KEY-N]` reference; none → message, exit 0; batches of 1,000; prints linked count and unresolved references per commit.
  - `ci pull-request --product <id> (--github | --number <n> --title <t> [--url] [--status open|closed|merged] [--body-file] [--repository] [--base-ref] [--head-sha] [--merge-sha])`: under `--github` exact bash parity with the monorepo pull-request step (status merged/closed/open, body only when non-empty, `mergeSha` only when merged, repository = `GITHUB_SERVER_URL/GITHUB_REPOSITORY`); prints linked features, unresolved references and `workOrderMergeError` as a warning.
  - Response check, parity with the monorepo `actions/lib/zensu-api.sh`: exactly one JSON value whose required fields exist with the right types (arrays not null); anything else fails with "check that the API URL points at the Zensu API" and a redacted, sanitized excerpt.
- **A4** `actions/coverage/action.yml`: inputs `api-key`, `api-url`, `product-id` (required); `coverage-file` (optional: without it the action only links commits and the PR); `coverage-format` (default `json-summary`); `root`; `version` (default the next release); `release-url` (mirrors). Steps: check configuration (skip with a notice on fork PRs) → install with the repository's own `install.sh` from the action checkout (`ZENSU_VERSION`, `ZENSU_INSTALL_DIR` under `$RUNNER_TEMP`, new `ZENSU_RELEASE_URL`; `install.sh` normalizes a version without `v`) → `ci commits --github --api-key-stdin` → `ci pull-request --github` on `pull_request` → `ci coverage --github` when a coverage file is given. Needs only sh/bash, curl or wget, tar and `sha256sum` or `shasum`, no jq. Linux and macOS runners; Windows fails with a pointer to `install.ps1` and the CLI. Inputs reach scripts only through `env`; the key never touches argv, disk or logs. Trust (2026-11-03): `install.sh` verifies the ed25519 signature of the checksums file against the release keys it embeds (a Go test keeps them equal to `internal/update/keys.go`) using OpenSSL 3 (`pkeyutl -verify -rawin`; on macOS the Homebrew `openssl@3` when the system one is LibreSSL), then the archive checksum. With `ZENSU_REQUIRE_SIGNATURE=1`, which the action sets, a missing signature or verifier fails the install; without it `curl | sh` verifies whenever OpenSSL 3 is present and otherwise says the signature was not checked. A wrong signature always fails. Tests run a copy of `install.sh` whose key line is replaced by a test key. `release.yml` fails before goreleaser when the action's `version` default differs from the tag; README "Releasing" says so and the guard names the fix.
- **A5** Tests: `ci` tests against fakeAPI (coverage body golden equal to the monorepo assertion, `--root`, `--json`, Markdown summary, `--github` defaults, 0 files, 10,001 files, malformed report, unsupported format, 400 with server message, `--api-key-stdin` with and without API URL); response-shape cases (HTML, null arrays, two JSON values, wrong types, missing fields); key never echoed; annotation injection; commits against a temporary git repository (`--range`, push, PR with head present and with only a merge commit, zero SHA, missing base, `GITHUB_SHA` wins over `HEAD`, 1,001 commits → two batches, right-trimmed messages, a range starting with `-`); pull-request (open/closed/merged events, empty body, flag mode, missing number/title); `action_test.go` (builds the CLI, serves it through a fake release via `ZENSU_RELEASE_URL`, runs each composite step with bash against a fake API; exact bodies, step summary, annotations, checksum mismatch fails, unconfigured skips, env-only inputs, key absent from output and disk); a CI job that runs `uses: ./actions/coverage` with an empty key; the docs tests force docs for the new group.
- **A6** README (command row; "Use in CI": GitHub action with SHA pin, GitLab/other CI with `--api-key-stdin`, mirrors via `release-url`; "Releasing" note), `skills/zensu/reference.md` `### ci`, `skills/zensu/SKILL.md` map.
- Verification (zensu-cli lines): `go vet ./...`, `GOOS=windows go vet ./...`, `go test -race ./internal/... -run 'CI|Login|ValidateAPIKey|Action|SkillDocs|Readme|Redact'`, then `go test -race ./...` once (repo gate); `sh -n install.sh`.

### Re-check (2026-11-15)
- origin/main is `d466f49` (#52 merged), the latest tag was `v0.21.0`, so the action's `version` default was `v0.22.0`; the re-check before the PR found `v0.22.0` published on 2026-11-16 from `d466f49` without the ci group, so the default and the README pin are `v0.23.0`.
- The monorepo reference was read at its origin/main `6fa9a4df7`: `actions/coverage/action.yml`, `actions/lib/zensu-api.sh`, `backend/internal/handlers/ci.go` (commit and pull request handlers, 1,000-commit limit), `backend/internal/handlers/feature_coverage.go` (files mode, 10,000-file limit, duplicate paths rejected), `backend/internal/handlers/coverage_action_test.go` (golden bodies and outputs), `backend/internal/server/server.go` (the `/api/ci` routes need scope `ci` or `write`, body limit 20 MiB), `backend/internal/middleware/scope.go` (the `403 forbidden` shape).
- `zensu.dev/install.sh` redirects to `install.sh` on zensu-cli `main`, so the new script reaches `curl | sh` users as soon as PR A merges; it must stay compatible with every published release.

### Design decisions
- **Credential redaction** happens where every API error is built: `readResponse` takes the credentials from the request that produced the response (`X-API-Key`, the bearer token of `Authorization`) and `apiError` replaces them, raw and JSON-escaped, in the raw body, the decoded message and the code. Every caller of `readResponse` is covered without call-site changes.
- **`--api-key-stdin`** reads at most 4 KiB from stdin, trims whitespace, refuses an empty key or one with spaces or control characters (without echoing it), and builds `client.New(&config.Config{APIKey: key}, apiURL, "")` with `apiURL` = `--api-url`, else `ZENSU_API_URL`, trailing slash removed. The stored login and `ZENSU_SESSION_TOKEN` are not consulted. `--body-file -` and `--api-key-stdin` both read stdin; `pull-request` refuses that combination before reading anything.
- **Annotations**: warnings go to stderr as `warning: <text>`, or `::warning::<encoded>` under `--github`. `WriteError` prints `::error::<encoded>` instead of `error: <text>` when the command that ran has `--github` set; the `--json` error line still follows. Encoding: `%` → `%25`, CR → `%0D`, LF → `%0A`. Server text inserted into any line passes `sanitizeTerminal`, and every output line starts with fixed text, so a response can never start a workflow command. The GitHub runner processes workflow commands on stdout and stderr.
- **Coverage report parsing** streams the top-level object to keep the entries in file order (the monorepo golden body lists them in document order); a duplicate key keeps its first position and its last value, as jq does. Counts must be non-negative integers; anything else is a malformed report and fails before any request. The report is read through the non-blocking regular-file open of #52 and capped at 64 MiB.
- **Coverage root**: `--root` (made absolute), else `GITHUB_WORKSPACE` under `--github`, else `git rev-parse --show-toplevel`, else the working directory. Both the root and its symlink-resolved form are tried as prefixes, so a relative `--root` under a symlinked working directory still matches the physical paths Node reports.
- **Skips under `--json`**: a coverage report without files prints `{"mappedFeatures":0,"entries":[],"unmappedPaths":[]}` and a range without references prints `{"processed":0,"links":[]}`, so `--json` always prints one JSON value. `ci commits --json` merges its batches into one `{"processed": N, "links": [...]}` object, each link kept verbatim.
- **`ci commits`** validates SHAs from the GitHub event as hex before asking git about them, refuses a `--range` that starts with `-`, and passes the range after `--end-of-options` (git ≥ 2.24 then refuses any option-like word instead of running it).
- **`ci pull-request`** has `--body` plus `--body-file` through `withValueFiles` (#52), so the body never passes the shell. The body is not rendered by Zensu (the server reads only its `[KEY-N]` references), so its usage names no Markdown/plain-text format. `--number` and `--title` end in `(required unless --github)`, a conditional marker the command contract leaves `required: false` (#50).
- **install.sh** lists the base64 Ed25519 public keys on the one `echo` line of a `release_keys` function (the secret gate refused a `RELEASE_KEYS="…"` assignment as a high-entropy credential, and an allow-marker comment would break the no-comments rule; the key is the public key of `internal/update/keys.go`); an Ed25519 SubjectPublicKeyInfo PEM is `MCowBQYDK2VwAyEA` + the base64 key, because the 12-byte DER prefix encodes to exactly 16 base64 characters. OpenSSL is found as `ZENSU_OPENSSL` (when set), else `openssl` on `PATH`, else the Homebrew `openssl@3` keg paths; only a binary whose `version` reads `OpenSSL 3` or later counts (LibreSSL cannot load Ed25519 keys, checked locally). The signature is checked before the archive is downloaded. `ZENSU_RELEASE_URL` needs an explicit `ZENSU_VERSION`, because a mirror cannot resolve `latest`.
- **Action**: the configuration step writes `configured=true|false` to `$GITHUB_OUTPUT`; every later step runs only when it is `true`. The install step puts the binary into a fresh `mktemp -d` directory under `$RUNNER_TEMP` and publishes its path as a step output, so the caller's `PATH` stays unchanged. The upload steps pipe the key with the `printf` builtin into `--api-key-stdin`; `ZENSU_API_URL` reaches the CLI through the step environment.
- **Action tests** live in `internal/ciaction` (a test-only package; `go build ./...` and `go vet` accept it, checked locally). They build the CLI once, publish a fake signed release, run every composite step with `bash --noprofile --norc -eo pipefail` like the runner, evaluate the few `if:` forms the action uses (an unknown form fails the test), and scan output and every temporary directory for the key.
- **release.yml guard** extracts the `version` default with `awk` and compares it with `github.ref_name`; a Go test runs that exact step script against the committed action for the matching and a mismatching tag.

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| FR-001 | `ValidateAPIKey` accepts a `403` whose code is `forbidden` as a restricted user key; other 403 codes stay rejected and `agent_key_not_allowed` stays the agent kind | spec A1 |
| FR-002 | `zensu auth login --with-token` with a restricted key stores it and says that only commands within its scopes work, naming `zensu ci` | spec A1 |
| FR-003 | Every `zensu ci` command accepts `--api-key-stdin`: the key comes from stdin, the client is built from it alone, the config is neither read nor written, and an API URL from `--api-url` or `ZENSU_API_URL` is required; no environment variable carries the key | spec A2 |
| FR-004 | Without `--api-key-stdin` the `zensu ci` commands use the normal login | spec A2 |
| FR-005 | CLI-wide, the text, plain text and raw body of an API error never contain the API key or token sent with the request | spec A2 |
| FR-006 | `zensu ci` with `coverage`, `commits` and `pull-request` is registered in the root command; `--product` is required on each; human output by default and `--json` | spec A3 |
| FR-007 | `--github` reads the CI context from the GitHub Actions variables named in A3; explicit flags win | spec A3 |
| FR-008 | Under `--github` warnings and errors are `::warning::`/`::error::` annotations with `%`, CR and LF encoded and server text sanitized; without `--github` the CLI prints no line that starts with `::`; a response holding `"\n::error::"` cannot start a workflow command | spec A3 |
| FR-009 | `ci coverage` reads an Istanbul json-summary (an object with `total`) and turns every other entry into `{path, linesCovered, linesTotal}` plus `branchesCovered/branchesTotal` when `branches.total > 0`; a malformed report or an unsupported `--format` fails before any request | spec A3 |
| FR-010 | Coverage paths are relative to `--root` (default: `GITHUB_WORKSPACE` under `--github`, else the git top-level, else the working directory), use forward slashes, and paths outside the root stay unchanged | spec A3 |
| FR-011 | `ci coverage`: 0 files → warning, no request, exit 0; more than 10,000 files → error; sends `POST /api/ci/coverage {productId, source:"ci", files, ciRunUrl?}`; prints recorded features and the unmapped count, warns when nothing mapped, and appends the Markdown summary table to `--markdown-summary` or `GITHUB_STEP_SUMMARY` | spec A3 |
| FR-012 | `ci commits` picks the range with exact parity to the monorepo commits step under `--github` (head, base, HEAD fallback, all-zero base dropped, missing base warning); `--range` wins; without flags it checks `HEAD` | spec A3 |
| FR-013 | `ci commits` reads `git log -z --format=%H%x1f%an%x1f%aI%x1f%B --end-of-options <range>`, right-trims messages, keeps only messages with a `[KEY-N]` reference, reports a range without references and exits 0, sends batches of at most 1,000 commits, and prints the linked count and unresolved references per commit | spec A3 |
| FR-014 | `ci pull-request` builds the body with exact parity to the monorepo pull-request step under `--github` and from `--number`, `--title`, `--url`, `--status`, `--body`/`--body-file`, `--repository`, `--base-ref`, `--head-sha`, `--merge-sha` otherwise; it prints linked features, unresolved references and `workOrderMergeError` as a warning | spec A3 |
| FR-015 | A 2xx answer that is not exactly one JSON value with the required fields of the right types (arrays not null) fails with "check that the API URL points at the Zensu API" and a redacted, sanitized excerpt, and writes no Markdown summary | spec A3 |
| FR-016 | `actions/coverage/action.yml` has the inputs and steps of A4: configuration check with a notice skip, install through the repository's `install.sh`, commits, pull request on `pull_request`, coverage when a file is given; `version` defaults to `v0.22.0` | spec A4 |
| FR-017 | In the action, inputs reach scripts only through `env`, the key reaches the CLI only on stdin through `--api-key-stdin`, a Windows runner fails with a pointer to `install.ps1`, and the steps need no jq | spec A4 |
| FR-018 | `install.sh` verifies the ed25519 signature of the checksums file against its embedded release keys with OpenSSL 3 or later (`pkeyutl -verify -rawin`, Homebrew `openssl@3` when the system OpenSSL is LibreSSL), then the archive checksum; a verifier counts only after it passes an Ed25519 known-answer test; `ZENSU_REQUIRE_SIGNATURE` (any value but empty or `0`) fails on a missing signature or verifier; otherwise it verifies whenever such an OpenSSL 3 is present and says when the signature was not checked; a missing signature of a release after v0.18.0 and a wrong signature always fail; a Go test keeps the embedded keys equal to `internal/update/keys.go` | spec A4, review R1-F1, R1-F2 |
| FR-019 | `install.sh` honours `ZENSU_RELEASE_URL` and normalizes a version without `v` | spec A4 |
| FR-020 | `release.yml` fails before goreleaser when the action's `version` default differs from the tag, and its message names the fix | spec A4 |
| FR-021 | The A5 tests exist: ci command tests, action tests against a fake signed release, and a CI job that runs `uses: ./actions/coverage` with an empty key | spec A5 |
| FR-022 | `docs/commands.json` contains the `ci` group, regenerated with `make commands` | spec |
| FR-023 | README (command row, "Use in CI" with SHA pin, other CI with `--api-key-stdin`, mirrors via `release-url`, "Releasing" note, annotations under Errors, the new install variables), `skills/zensu/reference.md` `### ci` and the `skills/zensu/SKILL.md` command map document the new group and pass the docs tests | spec A6 |
| AC-001 | `go vet ./...` exits 0 | spec Verification |
| AC-002 | `GOOS=windows go vet ./...` exits 0 | spec Verification |
| AC-003 | `go test -race ./internal/... -run 'CI|Login|ValidateAPIKey|Action|SkillDocs|Readme|Redact'` exits 0 | spec Verification |
| AC-004 | `sh -n install.sh` exits 0 | spec Verification |
| AC-005 | A built `zensu ci commits --github --api-key-stdin --product <id>`, with the key on stdin and `ZENSU_API_URL` at a local recording server, posts the referencing commits of a push event in a temporary git repository with the key in `X-API-Key`, and leaves an empty `ZENSU_CONFIG_DIR` empty | spec A2, A3 |
| AC-006 | `install.sh` with `ZENSU_REQUIRE_SIGNATURE=1` and `ZENSU_RELEASE_URL` at a local fake release installs a binary whose checksums carry a valid signature of the embedded key, and refuses the same release with a tampered signature | spec A4 |
| AC-007 | `zensu ci coverage --help` lists `--product` and `--file` with usage ending in `(required)`, and `zensu ci pull-request --help` lists `--body-file` | spec A3, #50, #52 |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` (go1.26.3) | present | n/a |
| git ≥ 2.24 (`--end-of-options`) | CLI | `git --version` (2.51.0) | present | n/a |
| bash | CLI | `/bin/bash --version` (3.2.57) | present | n/a |
| OpenSSL 3 or later | CLI | `/opt/homebrew/opt/openssl@3/bin/openssl version` (3.6.4), `openssl version` (4.1.0) | present | n/a |
| curl, tar, sha256sum/shasum | CLI | `command -v curl tar sha256sum shasum` | present | n/a |
| gh CLI with `repo` and `workflow` scope | CLI | `gh auth status` | present | n/a |
| Monorepo reference (read-only) | fixture | `git -C <monorepo> show origin/main:actions/coverage/action.yml` at `6fa9a4df7` | present | n/a |
| CLI acceptance driver | cli | `go run ./cmd/zensu ci --help` | present | n/a |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Login with a key without read scope | internal/auth/oauth_test.go, internal/cmd/login_test.go | - | [I] | 1 | FR-001, FR-002 |
| S2 | Feature | Redact sent credentials in API errors | internal/cmd/factory_test.go | - | [I] | 1 | FR-005 |
| S3 | Feature | `ci` group, credentials, annotations, response check | internal/cmd/ci_test.go | S2 | [I] | 1 | FR-003, FR-004, FR-006, FR-007, FR-008, FR-015 |
| S4 | Feature | `ci coverage` | internal/cmd/ci_coverage_test.go | S3 | [I] | 1 | FR-009, FR-010, FR-011, AC-007 |
| S5 | Feature | `ci commits` | internal/cmd/ci_commits_test.go | S3 | [I] | 1 | FR-012, FR-013, AC-005 |
| S6 | Feature | `ci pull-request` | internal/cmd/ci_pullrequest_test.go | S3 | [I] | 1 | FR-014, AC-007 |
| S7 | Feature | Signature-verifying install.sh | internal/ciaction/install_test.go | - | [I] | 1 | FR-018, FR-019, AC-004, AC-006 |
| S8 | Feature | Coverage action, release guard, CI smoke job | internal/ciaction/action_test.go | S4, S5, S6, S7 | [I] | 1 | FR-016, FR-017, FR-020, FR-021 |
| S9 | Feature | Docs and command contract | internal/cmd/skill_docs_test.go, internal/tools/cmdtree/main_test.go | S4, S5, S6, S8 | [I] | 1 | FR-022, FR-023, AC-001, AC-002, AC-003 |

### Step S1 — Login with a key without read scope
- **Covers**: FR-001, FR-002
- `internal/auth/oauth.go`: new kind `APIKeyKindRestricted`; a 403 with code `forbidden` returns it. `internal/cmd/login.go`: restricted-key message. Tests: a real-shape `forbidden` case in `TestValidateAPIKey` (the made-up `insufficient_scope` case stays rejected) and a stored-login case in `login_test.go`.

### Step S2 — Redact sent credentials in API errors
- **Covers**: FR-005
- `internal/cmd/factory.go`: `readResponse` collects the credentials of `resp.Request`; `apiError` redacts them in raw body, message and code. Test: non-2xx bodies that echo the API key (raw and JSON-escaped) and a bearer token, through `Factory.request` and the `--json` error line.

### Step S3 — `ci` group, credentials, annotations, response check
- **Covers**: FR-003, FR-004, FR-006, FR-007, FR-008, FR-015
- `internal/cmd/ci.go`: `NewCICmd`, shared flags, `--api-key-stdin` factory, reporter (warnings, annotation encoding), response-shape decoder, GitHub event reader; `internal/cmd/erroroutput.go`: `::error::` under `--github`; `internal/cmd/root.go`: registration.

### Step S4 — `ci coverage`
- **Covers**: FR-009, FR-010, FR-011, AC-007
- `internal/cmd/ci_coverage.go` and `internal/cmd/testdata/coverage-summary.json`.

### Step S5 — `ci commits`
- **Covers**: FR-012, FR-013, AC-005
- `internal/cmd/ci_commits.go`; tests against temporary git repositories.

### Step S6 — `ci pull-request`
- **Covers**: FR-014, AC-007
- `internal/cmd/ci_pullrequest.go`.

### Step S7 — Signature-verifying install.sh
- **Covers**: FR-018, FR-019, AC-004, AC-006
- `install.sh`: `RELEASE_KEYS`, OpenSSL lookup, signature check, `ZENSU_RELEASE_URL`, `ZENSU_REQUIRE_SIGNATURE`, `ZENSU_OPENSSL`, version normalization, download error messages. Tests in `internal/ciaction` with a patched copy and a fake release.

### Step S8 — Coverage action, release guard, CI smoke job
- **Covers**: FR-016, FR-017, FR-020, FR-021
- `actions/coverage/action.yml`, `.github/workflows/release.yml` guard step, `.github/workflows/ci.yml` smoke job, `internal/ciaction/action_test.go`.

### Step S9 — Docs and command contract
- **Covers**: FR-022, FR-023, AC-001, AC-002, AC-003
- `README.md`, `skills/zensu/reference.md`, `skills/zensu/SKILL.md`, `docs/commands.json` (regenerated), `docs/commands.md` if a rule changes.

**Checkpoint**: `go test -race <changed packages> -run '<step tests>'` over this phase's changed files and the suites that import or invoke them + `go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Delivery after the chain
Commit on branch `claude/ci-uploader-20835f`, push, open the PR (English, Conventional Commits, no monorepo paths or line numbers in commit or PR text), wait for green CI. Merging, the release tag and enabling immutable releases on `MKITConsulting/zensu-cli` stay with the user.

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
