# Zensu Plan: Release signature verification in install.ps1 (parity with install.sh)

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
Specification (zensu-cli worktree on branch `claude/trusting-robinson-43e746`, based on `origin/main` `bc15472`):

Bring `install.ps1` to parity with `install.sh` for release signature verification.

Requirements:
1. `install.ps1` verifies the Ed25519 signature of `zensu_<ver>_checksums.txt` (asset `zensu_<ver>_checksums.txt.sig`, one base64 signature per line; any line valid under any embedded key passes) BEFORE it downloads the archive, then checks the archive SHA-256 against the signed checksums.
2. A release after v0.18.0 without a signature always fails. An older unsigned release fails only when `ZENSU_REQUIRE_SIGNATURE` is set to any value but `0`; otherwise it prints a note and checks only the checksums.
3. A verifier counts only after the RFC 8032 Ed25519 known-answer test `install.sh` uses (test 2: key `PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw=`, message `r`), so a tool without Ed25519 is treated as missing, not as tampering. A missing verifier is fatal only under `ZENSU_REQUIRE_SIGNATURE`; otherwise a note.
4. Verifier: OpenSSL 3 or later (the same `pkeyutl -verify -rawin` invocation and version gate as `install.sh`). `ZENSU_OPENSSL` names the binary exclusively; otherwise `openssl` on PATH, then the OpenSSL that Git for Windows bundles. Research result: .NET has no Ed25519 signature API (dotnet/runtime#63174 is open with milestone Future; #14741 was closed on 2026-04-27 in favour of it), and no documented Windows CNG EdDSA support was found. GitHub's Windows images ship OpenSSL 3.6.5 in `C:\Program Files\OpenSSL\bin` on the machine PATH (runner-images readmes of image 20261116 and `Install-OpenSSL.ps1`). Git for Windows bundles OpenSSL 3.5.x (`mingw64\bin\openssl.exe` and `usr\bin\openssl.exe`; only MinGit leaves them out). A hand-rolled PowerShell Ed25519 would be an unvetted verifier, and Windows OpenSSH `ssh-keygen -Y verify` needs SSHSIG signatures the release does not produce.
5. Same messages, env vars and semantics as `install.sh`: `ZENSU_VERSION` (default `latest`, `v` prefix added, version charset check), `ZENSU_RELEASE_URL` (mirror `<url>/<tag>/<asset>`, needs `ZENSU_VERSION`), `ZENSU_REQUIRE_SIGNATURE`, `ZENSU_OPENSSL`, `ZENSU_INSTALL_DIR`. Errors must not exit the caller's session under `irm | iex`.
6. Keep the installer key list in sync with `internal/update/keys.go` and enforce it with a test, as `install.sh` is enforced.
7. Lift the Windows refusal in `actions/coverage/action.yml` only at full parity: on Windows the install step runs `install.ps1` with `ZENSU_REQUIRE_SIGNATURE=1` and must not change the runner's user PATH.
8. Update the README "Use in CI" and install sections and the skill docs where they mention the Windows limitation.
9. Tests mirror `internal/ciaction/install_test.go` for `install.ps1` under every available PowerShell (pwsh on Linux CI, pwsh and Windows PowerShell 5.1 on a new Windows CI job).
10. Verify against the published signed release v0.23.0 and against a tampered copy served from a loopback mirror via `ZENSU_RELEASE_URL`.

Constraints: conventional commits; no Claude attribution; no code comments; no `git stash`; public repo, so no private monorepo paths, structs or line numbers in commit or PR text; never push a tag or publish a release; the default `version` input of `actions/coverage/action.yml` stays `v0.23.0` (the next release-prep PR moves it). Run `go vet ./...` and `GOOS=windows go vet ./...` after finishing.

Delivery after the chain (not a requirement row, because the chain closes before it can be observed): commits on the feature branch, a pushed branch and a pull request against `main` with green CI that reports each check result; no tag and no release.

AI-work capture: product Zensu (binding reused), product maintenance with the reason "install.ps1 release signature verification and Windows support for the coverage action", confirmed by the user for this goal; run kind work.

Design decisions settled in Phase 1:
- Errors: under `-File` the script prints `error: <message>` to stderr and exits 1, like `install.sh`; under `Invoke-Expression` (no `$PSCommandPath`) it rethrows, so `irm | iex` never exits the caller's session. Notes print `Note: <message>.` to stderr, like `install.sh`.
- `ZENSU_NO_MODIFY_PATH` (any value but `0`) is new: the coverage action sets it so a runner's user PATH is never changed. Without it `install.ps1` adds the install directory to the user PATH as before.
- On Windows the action runs `install.ps1` with `pwsh`, or with Windows PowerShell when `pwsh` is missing, passing `-NoProfile -NonInteractive -ExecutionPolicy Bypass -File`, and normalizes paths with `cygpath -m`.
- The published-release check is a Go test gated by `ZENSU_TEST_PUBLISHED_RELEASE`, set in the new Windows CI job and in local runs; the regular suites stay hermetic.

Planning answers (2026-11-25): PowerShell 7 was missing locally, and the user chose to install it now (`brew install powershell`). The Windows-only criteria (AC-009, AC-010 and the Windows half of AC-011) cannot be observed on this Mac; the user chose to provide access to a Windows host.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5 (toolchain go1.26.3), stdlib `testing`, POSIX sh, PowerShell 5.1 and 7, GitHub composite action (bash) | **Coverage**: `go test -coverprofile=<scratchpad>/cover.out ./internal/... && go tool cover -func=<scratchpad>/cover.out` (changed non-test Go files) @ 90% lines (default-90%); the change edits no non-test Go file, so the table is expected to be empty

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | `install.ps1` run against a fake release whose checksums signature matches an embedded key prints "Verified the release signature." and installs `zensu.exe`; the archive is requested only after the signature check passed | spec |
| AC-002 | A checksums signature that matches no embedded key stops `install.ps1` with "error: the signature of zensu_<ver>_checksums.txt matches no zensu release key, so the download may have been tampered with" and exit 1, with and without `ZENSU_REQUIRE_SIGNATURE`; the archive is never requested and nothing is installed | spec |
| AC-003 | A missing signature of a release after v0.18.0 stops `install.ps1` with "error: could not download the signature zensu_<ver>_checksums.txt.sig of release v<ver>; every zensu release after v0.18.0 is signed, so nothing is installed without it" for `ZENSU_REQUIRE_SIGNATURE` `1`, empty and `0`; an unsigned v0.18.0 fails with "error: release v0.18.0 carries no signature (zensu_0.18.0_checksums.txt.sig)" when it is required (`1`, `yes`) and otherwise installs after "Note: release v0.18.0 carries no signature, so only its checksums are checked." | spec |
| AC-004 | A `ZENSU_OPENSSL` that is LibreSSL, an OpenSSL 3 without Ed25519, or a missing path counts as no verifier: with `ZENSU_REQUIRE_SIGNATURE=1` `install.ps1` stops with "error: verifying the release signature needs OpenSSL 3 or later with Ed25519 support; install it or set ZENSU_OPENSSL to its path", with `0` it installs after "Note: OpenSSL 3 or later with Ed25519 support was not found, so the release signature was not checked; set ZENSU_REQUIRE_SIGNATURE=1 to require it." | spec |
| AC-005 | An archive that differs from the signed checksums stops `install.ps1` with "error: checksum mismatch for <archive>" and installs nothing | spec |
| AC-006 | `install.ps1` refuses `ZENSU_RELEASE_URL` without `ZENSU_VERSION`, an invalid version and a mirror without the release with the `install.sh` messages; a version without the `v` prefix installs from the `v` tag; any valid line of a multi-line signature file passes; every embedded key is trusted | spec |
| AC-007 | A failing `install.ps1` run through `Invoke-Expression` raises a terminating error the caller can catch, and the caller's session goes on; run with `-File` it prints "error: <message>" and exits 1 | spec |
| AC-008 | The release keys `install.ps1` embeds equal `internal/update/keys.go`, and `install.ps1` and `install.sh` embed the same RFC 8032 Ed25519 known-answer vector, which verifies with Go's `crypto/ed25519` | spec |
| AC-009 | The `install.ps1` checks of AC-001 to AC-007 pass under Windows PowerShell 5.1 and PowerShell 7 on Windows | spec |
| AC-010 | On Windows with no `openssl` on PATH, `install.ps1` verifies the signature with the OpenSSL that Git for Windows bundles | spec |
| AC-011 | With `RUNNER_OS=Windows` the coverage action's install step runs `install.ps1` with `ZENSU_REQUIRE_SIGNATURE=1` and `ZENSU_NO_MODIFY_PATH=1`, the commits and pull-request steps run the installed `zensu.exe` against the API, the user PATH is left unchanged, and a release whose signature matches no key stops the action at the install step with no API call | spec |
| AC-012 | `install.ps1` installs the published v0.23.0 with `ZENSU_REQUIRE_SIGNATURE=1` after "Verified the release signature."; served from a loopback mirror through `ZENSU_RELEASE_URL`, an unaltered copy installs, while copies with altered checksums, an altered archive or a removed signature are refused with the AC-002, AC-005 and AC-003 messages; `install.sh` gives the same verdicts on the same copies | spec |
| AC-013 | `README.md` (the Windows install script section and "Use in CI") and `skills/zensu/reference.md` state that `install.ps1` verifies the release signature with OpenSSL 3 and that the coverage action runs on Windows runners, and no text says `install.ps1` skips the signature | spec |
| AC-014 | (deprecated) `go vet ./...`, `GOOS=windows go vet ./...` and `go build ./...` pass, and the full suite `go test -race ./...` is green on the final tree — a chain gate rather than feature behaviour: the terminus already requires the full-suite and lint evidence records on the shipped tree, and Phase 6 step 2 records the build | spec |
| FR-001 | `ZENSU_NO_MODIFY_PATH` set to any value but `0` makes `install.ps1` leave the user and session PATH unchanged; without it the install directory is added to the user PATH as before | spec |
| FR-002 | `install.ps1` keeps its other behavior: the default install directory `%LOCALAPPDATA%\Programs\zensu`, the arm64-to-amd64 note and the `irm \| iex` usage | spec |
| FR-003 | The default `version` input of the coverage action stays `v0.23.0` in this change | spec |
| FR-004 | The verifier is OpenSSL 3 or later only: `ZENSU_OPENSSL` exclusively when set, otherwise `openssl` on PATH, then Git for Windows' `mingw64` and `usr` OpenSSL under Program Files; each candidate must pass the known-answer test | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| Go toolchain | CLI | `command -v go` | present (go1.26.3) | — |
| OpenSSL 3 or later | CLI | `openssl version` | present (OpenSSL 4.1.0 on PATH; Homebrew openssl@3 3.6.4) | — |
| PowerShell 7 (`pwsh`) | CLI | `command -v pwsh` | missing at planning time | install (`brew install powershell`, user answer 2026-11-25) |
| GitHub CLI (pull request after the chain) | CLI | `command -v gh` | present | — |
| Published release v0.23.0 | endpoint | `curl -fsSI -L https://github.com/MKITConsulting/zensu-cli/releases/download/v0.23.0/zensu_0.23.0_checksums.txt.sig` | present (HTTP 200; release immutable) | — |
| Windows host with Windows PowerShell 5.1, PowerShell 7, Git for Windows and Git Bash (AC-009, AC-010, AC-011) | acceptance observability | a host the user makes reachable from this session | missing at planning time | provide access (user answer 2026-11-25) |
| Acceptance driver `cli` | CLI | `pwsh -NoProfile -File install.ps1` and `sh install.sh` against loopback mirrors | present once `pwsh` is installed | — |

## Manual Acceptance

| Criterion | Check a person runs before release | Why this environment cannot observe it | Consent |
|-----------|------------------------------------|----------------------------------------|---------|
| AC-009 | the CI job "Windows install script and coverage action" (windows-latest) on the release commit is green, with every TestInstallPS1 case passing under both pwsh and powershell and no skipped test | driver cannot run here: Windows PowerShell 5.1 and PowerShell 7 on Windows run only on a Windows host | chain-mode acceptance answer, 2026-11-25 |
| AC-010 | the CI job "Windows install script and coverage action" (windows-latest) on the release commit is green, with TestInstallPS1_FindsTheOpenSSLOfGitForWindows passing under both pwsh and powershell | driver cannot run here: Git for Windows and its bundled OpenSSL exist only on a Windows host | chain-mode acceptance answer, 2026-11-25 |
| AC-011 | the CI job "Windows install script and coverage action" (windows-latest) on the release commit is green, with both cases of TestCoverageAction_InstallsWithInstallPS1OnWindowsRunners (signed release, forged signature) passing | driver cannot run here: Git Bash, cygpath and the Windows user PATH exist only on a Windows host | chain-mode acceptance answer, 2026-11-25 |
| AC-012 | the CI job "Windows install script and coverage action" (windows-latest) on the release commit is green, with TestPublishedRelease_InstallPS1VerifiesItAndRefusesTamperedMirrors passing under both pwsh and powershell; and on macOS or Linux `ZENSU_TEST_PUBLISHED_RELEASE=<tag> go test ./internal/ciaction/ -run TestPublishedRelease -count=1` passes for install.sh and install.ps1 | non-loopback host: the check downloads the published release from github.com | chain-mode acceptance answer, 2026-11-25 |

## Cross-Layer Value Flow Pairings
(Per Principle 2 — Cross-Layer Value Flow Pairing. Omit table body if no pairings; keep the heading so Phase 6 audit can detect absence vs zero rows.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Signature verification, install.sh messages and env vars, iex-safe errors and `ZENSU_NO_MODIFY_PATH` in `install.ps1` | internal/ciaction/install_ps1_test.go | — | [I] | 1 | AC-001, AC-002, AC-003, AC-004, AC-005, AC-006, AC-007, FR-001, FR-002, FR-004 |
| S2 | Feature | Key and known-answer-vector sync tests for `install.ps1` | internal/update/install_keys_test.go | S1 | [I] | 1 | AC-008 |
| S3 | Feature | Shared test helpers: PowerShell discovery, zip releases, patched `install.ps1`, request paths, OpenSSL path | internal/ciaction/release_test.go | S1 | [I] | 1 | AC-001, AC-009 |
| S4 | Feature | `install.ps1` parity tests under every available PowerShell, including Git for Windows OpenSSL discovery | internal/ciaction/install_ps1_test.go | S1, S3 | [I] | 1 | AC-001, AC-002, AC-003, AC-004, AC-005, AC-006, AC-007, AC-009, AC-010, FR-001 |
| S5 | Feature | Coverage action Windows branch plus a Windows-capable action harness test that replaces the refusal test | actions/coverage/action.yml, internal/ciaction/action_test.go | S1, S3 | [I] | 1 | AC-011, FR-003 |
| S6 | Feature | Published-release test: v0.23.0 direct and from tampered loopback mirrors, for `install.ps1` and `install.sh` | internal/ciaction/published_release_test.go | S1, S3 | [I] | 1 | AC-012 |
| S7 | Integration | Windows CI job (`windows-latest`) running the Windows-relevant tests with the published-release check | .github/workflows/ci.yml | S4, S5, S6 | [W] | 1 | AC-009, AC-010, AC-011, AC-012, AC-014 |
| S8 | Feature | README and skill docs: Windows signature verification, env vars, coverage action on Windows | README.md, skills/zensu/reference.md | S1, S5 | [I] | 1 | AC-013 |

### Step S1 — `install.ps1` signature verification
- **Covers**: AC-001, AC-002, AC-003, AC-004, AC-005, AC-006, AC-007, FR-001, FR-002, FR-004
- Mirror `install.sh`: version resolution, mirror URL, checksums first, signature verification with the known-answer-gated OpenSSL, archive download, checksum check, extraction and install; `$ReleaseKeys` holds the keys of `internal/update/keys.go`.

### Step S2 — Key and vector sync tests
- **Covers**: AC-008
- Extend `internal/update/install_keys_test.go`: `install.ps1` keys equal `releaseKeys`; both scripts carry the same known-answer vector and it verifies with `crypto/ed25519`.

### Step S3 — Shared test helpers
- **Covers**: AC-001, AC-009
- `internal/ciaction/release_test.go`: `requireOpenSSL3` returns the binary it found, PowerShell discovery (pwsh everywhere, Windows PowerShell on Windows; fatal under CI when none), zip archives, Windows-named fake releases, a patched `install.ps1`, recorded request paths, the built CLI binary.

### Step S4 — `install.ps1` parity tests
- **Covers**: AC-001, AC-002, AC-003, AC-004, AC-005, AC-006, AC-007, AC-009, AC-010, FR-001
- `internal/ciaction/install_ps1_test.go`: every `install_test.go` case for `install.ps1`, plus the iex and `-File` error forms, `ZENSU_NO_MODIFY_PATH`, a temp directory with a space, the parser check and, on Windows, discovery of Git for Windows' OpenSSL.

### Step S5 — Coverage action on Windows runners
- **Covers**: AC-011, FR-003
- `actions/coverage/action.yml`: on Windows run `install.ps1` (pwsh, else Windows PowerShell) with `ZENSU_NO_MODIFY_PATH=1`, output `zensu.exe`. `internal/ciaction/action_test.go`: the harness runs the Windows branch, simulated with a stand-in `cygpath` off Windows and with Git Bash on Windows; the refusal test is replaced.

### Step S6 — Published-release test
- **Covers**: AC-012
- `internal/ciaction/published_release_test.go`, gated by `ZENSU_TEST_PUBLISHED_RELEASE`: the published release direct from GitHub and from loopback mirrors (unaltered, altered checksums, altered archive, removed signature), for `install.ps1` under each PowerShell and for `install.sh` where it runs.

### Step S7 — Windows CI job
- **Covers**: AC-009, AC-010, AC-011, AC-012, AC-014
- `.github/workflows/ci.yml`: a `windows-latest` job with pinned checkout and setup-go that runs the Windows-relevant tests of `internal/ciaction` and `internal/update` with `ZENSU_TEST_PUBLISHED_RELEASE=v0.23.0`.

### Step S8 — Docs
- **Covers**: AC-013
- `README.md` Windows install section and "Use in CI"; `skills/zensu/reference.md` install notes.

**Checkpoint**: `go test -race ./internal/ciaction/ ./internal/update/` over the changed tests + `go vet ./... && GOOS=windows go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines, default-90%)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
- Every `## Manual Acceptance` row carries the user's consent, and its check is carried into the chain-end report's `## Open` for a person to run before release
