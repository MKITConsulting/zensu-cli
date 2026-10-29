# Zensu Plan: `zensu update` – install-aware, signature-verified self-update

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
Today the CLI only has a passive, notify-only update hint (`internal/update/update.go`, June 2026): once per 24 h it asks
GitHub `releases/latest` and prints one stderr line that always says `curl -fsSL https://zensu.dev/install.sh | sh`.
There is no way to actually update from the CLI, and the hint is wrong for Homebrew (the cask installs
`Caskroom/zensu/<ver>/zensu`; curl|sh would put a shadowed second copy into `/usr/local/bin`), Windows, `go install`
and Docker.

Goal: one command `zensu update` that does the right thing for every install method; the hint points to it.

Decisions:
1. One command for all install methods; files owned by package managers are never overwritten (delegate or explain).
2. Signed releases are mandatory for the self-update: Ed25519 signature over the checksums file, public key(s)
   embedded in the CLI (a list, for rotation). A release without the signing secret fails (intended).
3. No background auto-install; the existing notice stays but says `run: zensu update`.
4. Homebrew: `zensu update` runs `brew update --quiet`, then `brew upgrade --cask mkitconsulting/tap/zensu`.
5. No automatic sudo; on missing write permission the error names `sudo zensu update`.

Behavior by install method (detected from the resolved `os.Executable` path):

| Install method | Detection | `zensu update` |
| --- | --- | --- |
| Homebrew cask | path contains `/Caskroom/zensu/` | delegate to the `brew` of the owning Homebrew prefix, then confirm the new version; that brew missing: print the command, exit 1 |
| Homebrew formula (retired) | path contains `/Cellar/zensu/` | print `brew uninstall --formula zensu && brew install --cask mkitconsulting/tap/zensu`, exit 1 |
| Install script / release binary | everything else, also inside a container | self-update |
| Docker image | the image sets `ZENSU_INSTALL_METHOD=docker` and runs `/usr/local/bin/zensu` (amended in review: generic container markers misclassified script installs inside containers) | print `docker pull ghcr.io/mkitconsulting/zensu-cli:latest`, exit 1 |
| System package manager | under `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin`, `/nix/store`, `/snap` | "update with your package manager", exit 1 |
| Development build | `version.Version == "dev"` | print `go install github.com/MKITConsulting/zensu-cli/cmd/zensu@latest` / rebuild, exit 1 |

Self-update flow: fresh `releases/latest` (no 24 h cache), validate and compare the tag; download fixed URLs like
`install.sh` (`zensu_<ver>_checksums.txt`, `.sig`, `zensu_<ver>_<os>_<arch>.tar.gz|.zip`; windows/arm64 uses amd64);
HTTPS only, redirects only to `github.com`, `release-assets.githubusercontent.com`, `objects.githubusercontent.com`;
size limits and an overall timeout; verify the signature against the embedded keys; verify the archive SHA-256;
extract only the `zensu`/`zensu.exe` regular-file entry (size limit) to a temp file in the target directory;
smoke test `<new> --version` must report `zensu <ver>`; swap: Unix atomic `os.Rename` (0755), Windows renames the
running exe to `.old`, moves the new file into place, rolls back on failure and removes `.old` on the next update;
any failure before the swap leaves the install untouched; the version cache is updated afterwards.

**Approach**: Vanilla implementation (strict TDD discipline not in effect for this chain) | **Tech Stack**: Go 1.25.5, cobra, stdlib `testing` + `httptest` | **Coverage**: `go test -coverprofile=cover.out ./internal/update/... ./internal/tools/... ./internal/cmd/... && go tool cover -func=cover.out` (changed files) @ 90% lines (default-90%)

## Requirements
| ID | Requirement | Source |
|----|-------------|--------|
| AC-001 | `zensu update --check --json` prints one JSON object with `currentVersion`, `latestVersion`, `updateAvailable`, `installMethod`, `updateCommand` and exits 0 without touching the installed binary | spec |
| AC-002 | For a script/binary install, `zensu update` replaces the binary with the latest release only after the checksums signature verifies against an embedded key and the archive SHA-256 matches; the new binary reports the new version | spec |
| AC-003 | A missing/invalid signature, checksum mismatch, failing smoke test or unwritable target leaves the installed binary byte-identical and exits non-zero with an actionable message (`sudo zensu update` on permission errors) | spec |
| AC-004 | For a Homebrew cask install, `zensu update` runs `brew update --quiet` then `brew upgrade --cask mkitconsulting/tap/zensu` with the `brew` of the Homebrew prefix that owns zensu, never writes the binary itself, and reports success only when that prefix's `zensu` then reports the new version; without that `brew` it prints the command and exits 1 | spec (amended in review) |
| AC-005 | Docker image, retired Homebrew formula, system-package-manager and development builds get the matching instruction and exit 1 without changes | spec (amended in review) |
| AC-006 | The passive notice says `run: zensu update` and is not printed after `zensu update` itself, but still after other commands (including `features update`) | spec |
| AC-007 | When the installed version is the latest, `zensu update` says so and exits 0 | spec |
| AC-008 | The release pipeline signs the checksums file: goreleaser produces `zensu_<ver>_checksums.txt.sig` from `ZENSU_RELEASE_SIGNING_KEY`, verifiable with the matching public key | spec |
| AC-009 | `update` is documented in the README command table, the reference.md group table and the SKILL.md command map, and the docs tests pass | spec |
| FR-001 | Install-method detection from the resolved executable path, the image's declared install method and the build version | spec (amended in review) |
| FR-002 | Release download over HTTPS with a redirect host allowlist, size limits and an overall timeout | spec |
| FR-003 | Ed25519 verification of the checksums file against a list of embedded public keys | spec |
| FR-004 | SHA-256 verification and single-entry extraction from tar.gz/zip with entry type and size checks | spec |
| FR-005 | Smoke test of the new binary: `--version` must report the expected version | spec |
| FR-006 | Atomic replacement on Unix; rename-aside with rollback on Windows; stale `.old` cleanup | spec |
| FR-007 | `internal/tools/releasesign`: `keygen` (private key file 0600, public key to stdout) and `sign` (key from the environment) | spec |
| FR-008 | The version-check cache is updated after a successful self-update | spec |

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` (go1.26.3 darwin/arm64) | present | n/a |
| goreleaser | CLI | `command -v goreleaser` (v2.16.0) | present | n/a |
| brew | CLI | `command -v brew` (7.0.11) | present | n/a — only for the dry-run cask-name check |
| gh | CLI | `command -v gh` | present | n/a — read-only release inspection |
| api.github.com releases/latest | endpoint | `curl -fsS --max-time 5 https://api.github.com/repos/MKITConsulting/zensu-cli/releases/latest` (tag v0.15.0) | present | n/a — live read-only verification only; tests use `httptest` |
| ZENSU_RELEASE_SIGNING_KEY | secret | `[ -n "${ZENSU_RELEASE_SIGNING_KEY:-}" ]` | missing | substitute=ephemeral test key for the local goreleaser snapshot (spec Verification); the GitHub secret is set by the user after merge (spec Out of scope) |
| cli acceptance driver | driver | `go build -o <scratch>/zensu ./cmd/zensu` | present | n/a |

## Cross-Layer Value Flow Pairings
(Vanilla mode: Principle 2 does not apply; heading kept so the audit can detect absence.)

## Status Legend
| [ ] Not started | [R] RED test | [I] Implemented | [G] GREEN | [RF] Refactored | [!] Blocked | [W] Wired |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts | Covers |
|------|------|-------------|-----------|------------|--------|----------|--------|
| S1 | Feature | Install-method detection (`internal/update/install.go`) | internal/update/install_test.go | – | [I] | 1 | FR-001, AC-005 |
| S2 | Feature | Release download, signature, checksum, extraction (`internal/update/selfupdate.go`, `internal/update/keys.go`) | internal/update/selfupdate_test.go | – | [I] | 1 | FR-002, FR-003, FR-004 |
| S3 | Feature | Smoke test, executable swap, cache refresh (`internal/update/selfupdate.go`; the Windows swap is selected with `runtime.GOOS` instead of separate build-tag files) | internal/update/selfupdate_test.go | S2 | [I] | 1 | FR-005, FR-006, FR-008, AC-002, AC-003, AC-007 |
| S4 | Feature | `zensu update` command with `--check`, `--json`, brew delegation (`internal/cmd/update.go`, `internal/cmd/root.go`) | internal/cmd/update_test.go | S1, S3 | [I] | 1 | AC-001, AC-004, AC-005, AC-007 |
| S5 | Integration | Notice text and suppression after `zensu update` (`internal/update/update.go`, `cmd/zensu/main.go`) | internal/update/update_test.go, internal/cmd/update_test.go | S4 | [W] | 1 | AC-006 |
| S6 | Feature | Release signing tool, goreleaser `signs`, release workflow secret, production public key (`internal/tools/releasesign`, `.goreleaser.yaml`, `.github/workflows/release.yml`, `internal/update/keys.go`) | internal/tools/releasesign/main_test.go | S2 | [I] | 1 | FR-007, AC-008 |
| S7 | Integration | Docs: README, `skills/zensu/SKILL.md`, `skills/zensu/reference.md` | internal/cmd/skill_docs_test.go | S4 | [W] | 1 | AC-009 |
| W1 | Integration | Phase 6 coverage gap tests: missing platform entry, missing archive, API cross-host redirect, malformed API body, releasesign flag/file errors | internal/update/selfupdate_test.go, internal/update/update_test.go, internal/tools/releasesign/main_test.go | S3, S6 | [W] | 1 | FR-002, FR-004, AC-003, FR-007 |

### Step S1 — Install-method detection
- **Covers**: FR-001, AC-005

### Step S2 — Release download, signature, checksum, extraction
- **Covers**: FR-002, FR-003, FR-004

### Step S3 — Smoke test, executable swap, cache refresh
- **Covers**: FR-005, FR-006, FR-008, AC-002, AC-003, AC-007

### Step S4 — `zensu update` command
- **Covers**: AC-001, AC-004, AC-005, AC-007

### Step S5 — Notice text and suppression
- **Covers**: AC-006

### Step S6 — Release signing
- **Covers**: FR-007, AC-008

### Step S7 — Docs
- **Covers**: AC-009

**Checkpoint**: `go test -race` over the changed packages (`./internal/update/... ./internal/cmd/... ./internal/tools/...`) + `go vet ./...` + `GOOS=windows go vet ./...` pass (the full suite runs in the Phase 6 audit, not here — unless the Phase 5 fallback fires)

## Final Verification
- All test suites pass, and the full suite is recorded green on the final tree by the evidence runner (`--evidence-run --scope full`)
- Coverage report generated for changed files (threshold: 90% lines)
- Every active `AC-###` criterion verified live on the final tree (`/zensu:verify-feature --chain`, one `--acceptance-record` per criterion); a dropped criterion keeps its row, marked `(deprecated) <text>`
- `go vet ./...`, `GOOS=windows go vet ./...`, `go build ./...`
- `goreleaser check` and one `goreleaser release --snapshot --clean --skip=docker,publish` with an ephemeral test key
- Live read-only: a local build with version 0.14.1 runs `zensu update --check --json` against GitHub; `zensu update` refuses the unsigned v0.15.0 without changes
- One-off network test against the real v0.15.0 archive (redirect, checksum, extraction, smoke test), removed afterwards
- `HOMEBREW_NO_AUTO_UPDATE=1 brew upgrade --cask --dry-run mkitconsulting/tap/zensu` confirms the cask name
