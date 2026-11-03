# Plan: Early AI-work scope and CLI compatibility

## Context
Update the installed CLI. Add early product/feature confirmation and compatibility checks in all five plugins, with regression tests and installation/update instructions including `zensu update`. Make the six existing PRs review-ready. Never transmit a work report to Zensu, bind this repository, or assign a feature in this session. Do not merge, release or deploy.

**Approach**: Vanilla implementation (TDD discipline disabled via hooks.tddImplementation).
**Tech Stack**: Go/Cobra and Node built-in tests. **Coverage**: Node experimental coverage and Go coverage; default 90% lines for changed executable files.

## Requirements
| ID | Requirement |
| --- | --- |
| AC-001 | Upgrade the installed CLI through its existing installation channel and verify the actual command surface. |
| AC-002 | A bounded, read-only CLI capability check detects missing commands/flags before capture writes and gives safe installation/update guidance without exposing stderr. |
| AC-003 | Before authorized implementation, explain missing product binding or per-goal feature scope and ask once for explicit confirmation; reuse valid bindings, never infer consent or fall back to cross-product work. |
| AC-004 | Explicit no-report instructions suppress questions and capture. A local pause preserves any existing pending receipt and prevents Stop submission. |
| AC-005 | Incompatibility and failed context verification preserve receipts, prevent submission and leave coding available. Late CLI downgrade is checked before checkpoint. |
| AC-006 | Native host identity, command authorization and deadlines remain intact in all five adapters; regression tests exercise actual adapter entry points. |
| AC-007 | Users get install/update instructions, including zensu update, old versions without that command, Homebrew ownership and verification of PATH-selected binaries. |
| AC-008 | Existing recovery regressions, relevant suites, builds, coverage and exact-head PR CI pass; no report, merge or deployment occurs. |

## Preconditions
| Name | Type | Verification | Status | Decision |
| --- | --- | --- | --- | --- |
| zensu | CLI | zensu --version and command help | present, outdated | update via Homebrew as explicitly requested |
| node | CLI | node --version | present | built-in tests/coverage |
| go | CLI | go version | present | CLI tests/build |
| pnpm | CLI | pnpm --version | present | Node packaging |
| gh | CLI | read six open PRs | present | update PRs and verify CI |
| update_plan | host tool | available tool metadata | unavailable | maintain this plan and commentary; do not invent a replacement tool |

## Cross-Layer Value Flow Pairings
No server schema or payload changes. Capability checks use actual CLI help; product/feature validation continues through the existing typed CLI context command. Both local receipt pause and its checkpoint consumer are tested.

## Steps
| Step | Type | Description | Depends On | Status |
| --- | --- | --- | --- | --- |
| UPDATE | Integration | Update the installed CLI and verify release capabilities | none | [W] |
| PREFLIGHT | Bug Fix | Shared bounded capability probe, read-only scope preflight and local pause | UPDATE | [W] |
| HOSTS | Integration | Wire all five native entry points and early-confirmation guidance | PREFLIGHT | [W] |
| DOCS | Integration | Install/update instructions, including older CLI recovery | UPDATE | [W] |
| VERIFY | Integration | Regression/coverage/build evidence and review preparation | HOSTS,DOCS | [W] |

## Final verification
Use only local fixtures for reporting tests. Verify command help against released CLI 0.18.0 and the outdated surface. Keep review results distinct from human acceptance. CLI 0.18.0 has capture flags but no `update`; the update command is already merged on main, not yet released. Do not claim it is installed.

## Delivery gates
Implementation verification is complete. The mandatory review chain and six current-head GitHub CI runs are checked after this local implementation audit and before final handoff. No merge or release is authorized by this task.
