# Zensu CLI reference

Companion to [SKILL.md](SKILL.md). Everything here was captured from the CLI's
own `--help` output — when in doubt, `zensu <group> [<command>] --help` is
authoritative.

## Install

| Method | Command |
| --- | --- |
| Homebrew (macOS/Linux) | `brew install --cask mkitconsulting/tap/zensu` |
| Script (Linux/macOS) | `curl -fsSL https://zensu.dev/install.sh \| sh` |
| Script (Windows) | `irm https://zensu.dev/install.ps1 \| iex` |
| Prebuilt binaries / Docker / Go | see the [repository README](https://github.com/MKITConsulting/zensu-cli) |

The macOS binary is Developer-ID signed and notarized. Pin a version with
`ZENSU_VERSION`, change the target dir with `ZENSU_INSTALL_DIR`.

## Auth & configuration

```bash
zensu auth login                        # browser OAuth2 + PKCE
zensu auth login --with-token zsk_xxx   # API key (CI / headless)
echo "$ZENSU_API_KEY" | zensu auth login --with-token -
zensu auth status                       # identity, host, token expiry (or the work order session)
zensu auth token                        # print token for scripting (refuses inside a work order session)
zensu auth logout
```

- Credentials: `hosts.json` under `$ZENSU_CONFIG_DIR`, else
  `$XDG_CONFIG_HOME/zensu`, else `~/.config/zensu` (mode `0600`).
- Host resolution: `--api-url` flag > `ZENSU_API_URL` env > stored host >
  `https://api.zensu.dev`.
- Self-hosted deployments discover OAuth endpoints via
  `/.well-known/oauth-authorization-server` (fallback
  `/oauth/authorize` + `/oauth/token`).
- Work order sessions: `ZENSU_SESSION_TOKEN` (a `zst_…` token from a claim)
  overrides any stored login for every command. It needs an explicit host
  (`--api-url`, `ZENSU_API_URL` or a stored host); the built-in default host is
  refused. Inside a session `auth token` refuses and `auth status` reports the
  session instead of a login. A `401 invalid_session_token` or `409
  stale_attempt` means stop, not log in.

Global flags on every command: `--api-url <url>`. Typed commands accept
`--json` for raw JSON output.

## Command groups

| Group | Manages |
| --- | --- |
| `auth` | Authentication against a Zensu host |
| `products` | Products, visions, bootstrap, repository import |
| `features` | Features: CRUD, status, history, revision, merge/split, deprecate |
| `subfeatures` | Sub-features of a feature |
| `tiers` | Pricing tiers and per-feature tier availability |
| `roadmap` | Roadmaps and milestones |
| `journeys` | User journeys, steps, health analysis |
| `security` | Classification, security tests, reviews, score, posture |
| `ghost` | Ghost scans and feature candidates (brownfield import) |
| `pulse` | Development sessions |
| `work` | Work orders: dispatch, claim, session reporting, questions, human decisions, policy, repositories |
| `plan` | Work plans: push plan files, follow status, approve, finalize, abandon, follow-ups |
| `link` | Link tests, docs, and source files to a feature |
| `knowledge` | Organization knowledge pool search |
| `design` | Product design-system context |
| `mocks` | Feature design mocks — upload and inspect |
| `wiki` | Wiki pages |
| `org` | Organization users |
| `doc` | Documentation context and CLAUDE.md templates |
| `meta` | Informational stubs only — these verbs live in the Zensu MCP server / agent plugins, not the REST API |
| `completion` | Shell completion (bash/zsh/fish/powershell) |

## Commands and verified flags

### products

```
zensu products list [--json]
zensu products get <product-id> [--json]            # response embeds components[]
zensu products create --name <name> [--slug s] [--type public|internal|hybrid] [--description d]
zensu products import <product-id> --repo-url <url> [--repo-type github|gitlab|bitbucket|local]
zensu products vision-create --title <t> --content <markdown>
    [--product <uuid>] [--source studio|import|claude-code]   # omit --product for greenfield visions
zensu products vision-get <vision-id>
zensu products bootstrap-apply <vision-id>          # payload: {"components":[{name,slug,description}],
                                                    #   "features":[{title,slug,description,component,
                                                    #   priority,estimatedEffort,securityClassification,
                                                    #   securityReasoning,featureScope,subfeatures}]}
zensu products bootstrap-step <vision-id> <step>    # mark a post-bootstrap step completed
```

### features

```
zensu features list --product <id> [--status planned|in-progress|testing|released] [--json]
zensu features get <feature-id> [--json]
zensu features create --product <id> --component <id> --title <t> [--slug s] [--status s] [--json]
zensu features update <feature-id> [--title t] [--description d] [--priority low|medium|high|critical]
zensu features status <feature-id> <new-status>     # dedicated transition command
zensu features history <feature-id> [--json]
zensu features revision <feature-id> --scope-summary <s> [--scope-details d]
    [--estimated-effort S|M|L|XL] [--coverage-target 0-100] [--docs-required]
    [--target-release v] [--assignee a] [--created-by api|mcp|web-ui|github-sync]
zensu features merge <target-feature-id> --source '<json-uuid-array>' --title <t> --slug <s> [--reason r]
zensu features split <feature-id> --children '<json>' [--reason r]   # children: [{title, slug}]
zensu features deprecate <feature-id> [--reason r] [--replacement <uuid>] [--removal-planned-at RFC3339]
```

### subfeatures

```
zensu subfeatures add <feature-id> --title <t> [--slug s] [--description d]
    [--priority critical|high|medium|low] [--status s] [--assignee a]   # status defaults to planned
zensu subfeatures list <feature-id> [--compact]     # compact: id, slug, title, status, priority only
zensu subfeatures promote [feature-id] <subfeature-id>   # promote to top-level feature
```

### link

```
zensu link test <feature-id> --file <path> --test-type unit|integration|e2e|security|performance|accessibility
    [--function name] [--last-run-status passed|failed|skipped]
zensu link docs <feature-id> --doc-type <type> [--audience <a>] [--file path]
    [--external-url url] [--content markdown]       # --content creates/updates the linked wiki page
zensu link source <feature-id> --file path[:type[:language]]   # repeatable
```

### security

```
zensu security classify <feature-id> [--classification public|internal|confidential|restricted]
    [--data-sensitivity none|pii|financial|health|credentials]
    [--auth-required] [--auth-type jwt|api-key|oauth2|none]
    [--encryption-at-rest] [--encryption-in-transit] [--input-validation]
    [--rate-limited] [--audit-logged]
    [--pentest-status not-required|pending|passed|failed]
    [--threat-model-status not-required|pending|completed]
zensu security add-test <feature-id> --file <path>
    --type auth-bypass|injection|access-control|rate-limit|input-validation|data-exposure|header-security|dependency-scan|csrf|xss|ssrf
    [--owasp-id A01:2021] [--last-run-status passed|failed|skipped]
zensu security review <feature-id> --reviewer <who> --status approved|rejected|conditional
    [--type manual|automated|external] [--findings f] [--conditions c]
zensu security score <feature-id>                   # score + release-gate state
zensu security validate <feature-id>                # release requirements met?
zensu security analyze <feature-id>                 # detailed single-feature analysis
zensu security posture <product-id>                 # product-wide aggregate
zensu security suggest-tests <feature-id>           # context for test selection
zensu security threat-model <feature-id>            # STRIDE context data
```

Classification is ordered `public < internal < confidential < restricted`;
higher classifications raise the release-gate requirements. Classify sets the
attributes and recalculates the security score automatically.

### ghost

```
zensu ghost scan --product <id> --candidates '<json-array>' [--components '<json-array>']
    [--repo-url u] [--branch b] [--source api|mcp|web_ui]   # see --help for the candidate shape
zensu ghost candidates <scan-id>                    # ordered by confidence
zensu ghost approve <scan-id> <candidate-id> --product <id>
zensu ghost reject <scan-id> <candidate-id> --product <id> [--reason r]
zensu ghost batch <scan-id> --product <id> [--approve-ids '<json-uuids>']
    [--reject-ids '<json-uuids>'] [--reject-reason r]
zensu ghost apply <scan-id> --product <id> [--enrich-existing]
```

`apply` creates features, components, and links test/doc/source files. Use
`--enrich-existing` by default — it links discovered artifacts to matching
existing features (by slug) instead of failing on slug conflicts. Omit it only
on the very first scan of an empty product.

### pulse

```
zensu pulse start --head-sha <sha> [--branch b] [--product uuid] [--project /abs/path]
zensu pulse start --work-order <id> [--head-sha sha] [--branch b]   # inside a work order session
zensu pulse end <session-id> [--changed-files "a.ts,b.go"]
zensu pulse summary <session-id>
```

Sessions are idempotent per HEAD SHA — `start` with the same SHA continues the
existing session. With `--work-order` the session authenticates with
`ZENSU_SESSION_TOKEN`, is idempotent per attempt, lands in the Pulse journal of
the agent key's creator and honors that person's tracking opt-out
(`{"status":"tracking_disabled"}`, also the answer when the key's creator is no
longer an active member). Zensu ends it when the attempt ends.

### work

Human decisions need the browser login (`zensu auth login`); API keys and MCP
clients get `403 interactive_user_required`, agent keys `403
agent_key_not_allowed`. Drafting (`work create`, `plan push`) needs a signed-in
person too. Worker verbs need an agent key (scope `agent` only) and refuse a
session token. Session verbs need `ZENSU_SESSION_TOKEN`. A claim returns
nothing until the product's automation policy allows the agent key
(`zensu work policy set --product <product id> --add-allowed-key <key id>`).
`--add-allowed-key` and `--remove-allowed-key` change the key list the command
just read and may be combined; `--allowed-key` replaces the whole list and
combines with neither. In text mode `policy set` prints the resulting
`Allowed agent keys:` (or `none`). The server drops stored keys that are no
longer active and refuses newly named keys that are not active agent keys
(`422 invalid_agent_keys`); the error names the flag that supplied them.
`policy set` writes the policy back with the version it read; when someone
changed the policy in between (`409 policy_changed`), it reads the policy again
and repeats the change. It makes at most three attempts and notes each repeat on
stderr. A server from before this check refuses the version
(`400 invalid_body`); the command then writes once without it and says on stderr
that a simultaneous change is not detected.

```
# humans (browser login; read commands also work with an API key)
zensu work create --product <id> --feature <KEY-N|uuid> --repository <url> [--base-branch main] [--harness claude]
zensu work list [--product id] [--plan id] [--feature KEY-N|uuid] [--status s] [--page n] [--per-page n]
zensu work get <work-order-id> --json
zensu work events <work-order-id>
zensu work approve <work-order-id>
zensu work requeue <work-order-id>
zensu work cancel <work-order-id> [--confirm-pr-closed]
zensu work confirm-merge <work-order-id> --head-sha <full-sha> [--merge-sha <sha>]
zensu work answer <question-id> --answer "..." [--rationale "..."] [--plan-wide]
zensu work overturn <question-id> --answer "..." [--rationale "..."]
zensu work questions --plan <plan-id> [--order id] [--status open|awaiting_human|answered] [--scope package|plan]
zensu work policy get --product <id>
zensu work policy set --product <id> [--plan-approval required|auto] [--max-running-orders n] [--lease-minutes n] [--max-order-cost-usd n | --clear-max-order-cost] [--allowed-key <key-id>... | --add-allowed-key <key-id>... --remove-allowed-key <key-id>...]
zensu work repositories list --product <id>
zensu work repositories add --product <id> --repository https://github.com/acme/app [--risk-path 'backend/internal/auth/**']
zensu work repositories update <repository-id> --product <id> (--risk-path '...'... | --clear)
zensu work repositories remove <repository-id> --product <id>

# workers (agent key)
zensu work claim --session-id <sid> --repository <url>... [--kind implement] [--wait 0-50] --json   # prints the session token
zensu work confirm <work-order-id> --session-id <sid> --attempt <n>
zensu work release <work-order-id> --session-id <sid> --attempt <n> --outcome success|failure|rate_limited|runtime_cap|interrupted [--not-before <rfc3339>]   # --not-before only and always with rate_limited
zensu work usage <work-order-id> --session-id <sid> --attempt <n> [--cost-usd 1.2] [--turns 40] [--input-tokens n] [--output-tokens n]

# sessions (ZENSU_SESSION_TOKEN)
zensu work heartbeat <work-order-id>
zensu work event <work-order-id> --stage planning|implementing|pr_open|reviewing|validating|ready_for_merge [--pr-url u] [--pr-number n] [--branch b] [--head <full-sha>] [--client-event-id id]
zensu work event <work-order-id> --artifact plan|report|evidence|pr|other (--url u | --path p) [--summary "..."]
zensu work event <work-order-id> --blocked plan_approval|worker_error --reason "..."
zensu work ask <work-order-id> --category clarification|technical_choice|product_decision|scope|risk|external --question "..." [--option a --option b] (--blocking | --default-option a) [--requirement AC-001]
zensu work followup <work-order-id> --title "..." --rationale "..." --severity low|medium|high|critical [--path p]...
```

When the product requires a plan approval (the default), a session moves to
`--stage planning`, attaches the plan with `--artifact plan --url <link>`,
blocks with `--blocked plan_approval --reason "..."` (which ends the session)
and stops. A person runs `zensu work approve <work-order-id>`; the order is
queued again and the next claim implements. `--stage implementing` before that
answers `409 plan_approval_required`. A stage event carries no `--detail`.

Events and questions carry a client event id; pass `--client-event-id` to make
retries idempotent. A `409 stale_attempt` means the lease belongs to another
attempt, and a `401 invalid_session_token` means the session has ended — stop
without further forge writes and never log in. `--session-id` is 8 to 128
characters of letters, digits, `.`, `_`, `:` and `-`; ID flags take UUIDs, and
feature keys may be written in any case (`zen-42`). Only metadata and short prose are
accepted (`422 content_not_allowed` for code, commands, logs or diffs).

### plan

```
zensu plan push .zensu/plans/checkout.md [--product id] [--dry-run] [--json]
zensu plan status <plan-id> [--watch] [--interval 10s]
zensu plan list --product <id> [--status draft|decomposing|graph_proposed|open|integrating|held|verifying|final_pr_open|merged|abandoned]
zensu plan approve <plan-id>
zensu plan finalize <plan-id> [--pr-url u --pr-number n --head-sha <full-sha>]
zensu plan abandon <plan-id> [--confirm-pr-closed]
zensu plan confirm-merge <plan-id>
zensu plan followups <plan-id>
zensu plan followup accept <plan-id> <followup-id>
zensu plan followup dismiss <plan-id> <followup-id>
```

A plan file is Markdown with a YAML front matter (`product`, `repository`,
`base_branch`, `name`, `items`); each item names `feature` (KEY-N or UUID),
`revision` (UUID), or `title` + `component` for a new feature, plus
`requirements` (`AC-001`, `FR-001`, `IF-001`, optionally `{id, tags, text}`) and
`paths` (clean relative file paths without wildcards, at most 100 per item).
`new_revision: true` with `scope_summary` plans a new revision of a shipped
feature. `scope_summary` belongs only to a `title` item or to a `feature` item
with `new_revision: true`; on a `revision` item or on a `feature` item without
`new_revision` the file is refused. `task` (a task UUID) links the work order of
a one-item plan, so a plan with several items and a `task` is refused before
any request. Requirement texts come from the `## Requirements` table (cells
split on unescaped `|`, fenced code is skipped) or from `text`; they become the
description and criteria of a new feature or revision, and for an existing
feature the push notes that they are ignored, because a plan records only
requirement IDs and tags. Requirement IDs must be unique across the plan. The
push is idempotent on the SHA-256 of the file's text (a BOM and CRLF line
endings do not change it), and the recorded source path is relative to the
root of the repository that holds the file.

Before its first write the push checks everything Zensu would refuse later: the
repository is registered for the product, the task exists, every feature and
revision belongs to the product and is a dispatchable top-level feature, a
reused slug is a top-level feature in the item's component with an open
revision, components belong to the product, descriptions fit 10000 characters
and no two items target the same feature. It also reads the product's live
plans (every status but `merged` and `abandoned`) and refuses a target revision
— the active revision of a feature item or of a reused feature, or a `revision`
item — that is an open item of one of them: a revision sits in one live plan at
a time. The error names the item, the revision and the holding plan; run
`zensu plan abandon <plan-id>` first. A live plan pushed from the same source
path with other content is named as well, in the error or as a note, because
the push then re-pushes an edited file. New features and new revisions cannot
conflict. When the push creates features, it reads `GET /api/billing/usage`
and refuses when they would exceed the organization's feature allowance
(`-1` is unlimited; a `403` or `404` skips the check, the server still enforces
it). `--dry-run` runs the same checks, reports the same refusals and writes
nothing. With `--json` every mode prints one document:
`{"dry_run", "already_pushed", "notes", "request" (dry run only), "plan"}`.
`plan status --watch` retries a poll that times out, meets a refused or reset
connection, loses the response mid-body or gets `429` or a `5xx`, with backoff,
and prints one line per retry to stderr (`poll 2 of 5 failed: …; retrying in
20s`); five failed polls in a row end the watch. Any other error ends it at the
first poll, among them TLS and certificate failures, a refused cross-host
redirect and an invalid API URL. `plan followups` lists each follow-up with its
`STATE` (open, accepted, dismissed). See `zensu plan push --help` for a full
example.

### tiers / roadmap / journeys

```
zensu tiers create --product <id> --name <n> --slug <s> --tier-order <int>
    [--description d] [--color c] [--default]       # tier-order: 1 = lowest, ascending
zensu tiers list --product <id>
zensu tiers matrix --product <id>                   # complete tier matrix
zensu tiers set-feature <feature-id> --tiers '<json>'
    # entries: [{"tierId": ..., "gatingType": "hard|soft|preview", "tierLimits": {...}?}]

zensu roadmap create --product <id> --title <t> [--period '2026-Q2'] [--goal g]...
    [--description d] [--status draft|active|completed|archived]
zensu roadmap list --product <id>
zensu roadmap get <roadmap-id>
zensu roadmap update <roadmap-id> --title <t> [--period p] [--goal g]... [--description d] [--status s]
zensu roadmap delete <roadmap-id>                   # linked features survive, membership only
zensu roadmap add-feature <roadmap-id> --feature <id>
    [--start-period '2026-Q2'] [--end-period '2026-Q4'] [--sort-order n]
zensu roadmap remove-feature <roadmap-id> <feature-id>
zensu roadmap milestone-create <roadmap-id> --title <t> [--period '2026-Q3'] [--status planned|done]
zensu roadmap milestone-list <roadmap-id>
zensu roadmap milestone-delete <roadmap-id> <milestone-id>

zensu journeys create --product <id> --title <t> [--slug s] [--description d]
    [--persona p] [--priority critical|high|medium|low]
    [--type critical|happy_path|edge_case|error_path|onboarding] [--tier <uuid>]
zensu journeys list --product <id>
zensu journeys get <journey-id>
zensu journeys step <journey-id> --product <id> --title <t> --step-order <int>
    [--description d] [--expected-result r] [--feature <uuid>] [--critical]
    [--interaction-type action|navigation|input|validation|output|wait]
zensu journeys steps <journey-id>                   # list steps
zensu journeys step-update <journey-id> <step-id> --product <id> [--title t] [--step-order <int>]
    [--description d] [--expected-result r] [--feature <uuid>] [--critical]
    [--interaction-type action|navigation|input|validation|output|wait]
    # read-modify-write, last writer wins; omitted flags are resent unchanged
zensu journeys step-delete <journey-id> <step-id> --product <id>
    # remaining steps keep their order, so reorder afterwards if it must stay gap-free
zensu journeys health <journey-id>                  # health analysis
zensu journeys suggest --product <id>               # context to suggest journeys
```

### knowledge / design / mocks / wiki / org

```
zensu knowledge search --query "<text>" [--limit 1-50] [--scope org|personal]
zensu knowledge get <item-id>
zensu knowledge sources

zensu design context <product-id> [--component <id>]   # Design.md, shared CSS, assets

zensu mocks create <feature-id> <file> [--title t] [--alt-text a]
    # file extension picks the type: .png/.jpg/.jpeg -> image, .html/.htm -> html
    # 32 MiB default; ZENSU_MAX_UPLOAD_BYTES raises it, capped at 512 MiB because the
    # body is assembled in memory. Responses have their own bound, ZENSU_MAX_RESPONSE_BYTES
    # (64 MiB default), applied to every response a subcommand reads.
zensu mocks list <feature-id>
zensu mocks get <feature-id> <mock-id>              # metadata or raw content

zensu wiki create --product <uuid> --title <t> --content <markdown>
    [--doc-type <type>] [--audience <a>] [--visibility public|private]
    [--entity-type feature|component|product --entity-id <uuid>]   # visibility defaults to private
zensu wiki list [--product <uuid>] [--audience <a>] [--parent <uuid>]
zensu wiki update <page-id> [--title t] [--content markdown] [--change-summary s]
    [--visibility public|private]

zensu org users [--query "name-or-email"]           # omit --query to list all members
```

### meta

These verbs are informational stubs in the CLI: the work happens in the Zensu MCP
server or the host's Zensu plugin, and each command explains where to go instead.

```
zensu meta workflow-guide <workflow>                # bootstrap|security-review|implement|pulse|ghost-scan
zensu meta suggest-workflow --product <uuid>
zensu meta scaffold-agent [--cli claude-code|kiro|cursor|copilot|all]
```

### doc

```
zensu doc claude-md --product <id> --variant full|minimal|ci-only
zensu doc claude-md-context <product-id>            # aggregate behind the template
zensu doc gen-context <feature-id>                  # rich context for doc authoring
```

`claude-md` variants: `full` (active feature development), `minimal`
(library/infra repos), `ci-only` (CI/CD integration only).

## Enum vocabularies

| Domain | Values |
| --- | --- |
| Feature status | `planned` `in-progress` `testing` `released` |
| Priority | `low` `medium` `high` `critical` |
| Estimated effort (bootstrap) | `S` `M` `L` `XL` |
| Product type | `public` `internal` `hybrid` |
| Feature scope (bootstrap) | `public_facing` `internal_only` |
| Test type (`link test`) | `unit` `integration` `e2e` `security` `performance` `accessibility` |
| Security test type (`security add-test`) | `auth-bypass` `injection` `access-control` `rate-limit` `input-validation` `data-exposure` `header-security` `dependency-scan` `csrf` `xss` `ssrf` |
| Last run status | `passed` `failed` `skipped` |
| Doc type | `user_facing` `api_reference` `tutorial` `adr` `release_notes` `internal` `migration_guide` `overview` |
| Audience | `end_user` `developer` `admin` `internal` |
| Security classification | `public` `internal` `confidential` `restricted` |
| Data sensitivity | `none` `pii` `financial` `health` `credentials` |
| Auth type | `jwt` `api-key` `oauth2` `none` |
| Pentest status | `not-required` `pending` `passed` `failed` |
| Threat-model status | `not-required` `pending` `completed` |
| Review status / type | `approved` `rejected` `conditional` / `manual` `automated` `external` |
| Ghost scan source | `api` `mcp` `web_ui` |
| Wiki visibility | `public` `private` |
| Knowledge scope | `org` `personal` |
| CLAUDE.md variant | `full` `minimal` `ci-only` |

## Scripting recipes

```bash
# all feature ids currently in testing
zensu features list --product "$PRODUCT" --status testing --json | jq -r '.[].id'

# component ids of a product
zensu products get "$PRODUCT" --json | jq -r '.components[].id'

# raw API call when no typed command exists (prefer typed commands)
curl -sH "Authorization: Bearer $(zensu auth token)" "${ZENSU_API_URL:-https://api.zensu.dev}/api/..."
```

## Shell completion

```bash
zensu completion zsh|bash|fish|powershell   # see README for per-shell setup
```
