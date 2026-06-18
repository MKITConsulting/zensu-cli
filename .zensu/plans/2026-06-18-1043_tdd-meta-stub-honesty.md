# TDD Plan: Honest help + dead-flag removal for MCP-only meta stubs

## Context
Fix the three MCP-only stub commands in `internal/cmd/meta.go` (`scaffold-agent`, `suggest-workflow`, `workflow-guide`) so their help text is honest and dead flags are removed. Each command's `RunE` unconditionally returns a "not available over REST" error, yet the Long/Short help advertises working functionality and each registers a dead `--json` flag (`var asJSON bool` + `cmd.Flags().BoolVar(&asJSON, "json", ...)` but never read — confirmed the only dead `--json` flags in the CLI; every other command file wires `if asJSON {`).

Changes:
1. All three: rewrite Long to clearly state the command is NOT available over the REST CLI and point to the Zensu MCP server (and for scaffold-agent the zensu-claude-code plugin), while still describing what the tool does. Keep the existing error messages as-is.
2. All three: append " (MCP-only)" to the Short description so `zensu meta --help` flags them at a glance.
3. All three: remove the dead `--json` flag — delete both the `var asJSON bool` declaration and the `cmd.Flags().BoolVar(&asJSON, "json", ...)` registration.
4. Keep functional input flags: `--cli` on scaffold-agent and `--product` on suggest-workflow.

Out of scope: no other command files (only meta.go has this stub pattern).

**Approach**: Vanilla implementation (TDD discipline disabled via hooks.tddImplementation) | **Tech Stack**: Go 1.25.5, cobra CLI | **Coverage**: `go test -coverprofile=cover.out ./internal/cmd/ && go tool cover -func=cover.out` @ 90% (default-90%)

## Preconditions
| Name | Type | Verification | Status | Decision |
|------|------|--------------|--------|----------|
| go | CLI | `command -v go` | present | n/a |

## Cross-Layer Value Flow Pairings
(No pairings — change is local to one file's help strings + flag registrations; no value crosses a process/persistence/network boundary.)

| Feature Step | New Value | Unchanged Layer (file / module) | Characterization Step | Seam Asserted |
|--------------|-----------|---------------------------------|------------------------|----------------|

## Status Legend
| [ ] Not started | [I] Implemented | [G] GREEN | [W] Wired | [!] Blocked |

## Steps
| Step | Type | Description | Test File | Depends On | Status | Attempts |
|------|------|-------------|-----------|------------|--------|----------|
| S1 | Feature | meta.go: honest Long + Short "(MCP-only)" + remove dead --json on all three stub commands | internal/cmd/meta_test.go | - | [I] | 1 |
| S2 | Feature | meta_test.go: regression test — --json rejected on scaffold-agent/suggest-workflow/workflow-guide | internal/cmd/meta_test.go | S1 | [I] | 1 |

### Step S1 — Rewrite the three MCP-only stub commands in meta.go
Honest Long text (MCP-only, not REST), Short + " (MCP-only)", drop dead `--json` var + registration. Keep `--cli` and `--product`. Error messages unchanged.

### Step S2 — Regression test locking in the dead-flag removal
Assert `--json` is no longer an accepted flag on the three meta commands (cobra returns "unknown flag"). Existing error-behavior tests must still pass.

**Checkpoint**: `go build ./...` + `go vet ./...` + `go test ./internal/cmd/` pass

## Final Verification
- [x] All test suites pass (`go test -race ./...` — all packages ok)
- [x] Coverage report generated for changed files (meta.go 100% all funcs, threshold: 90%)
