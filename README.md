# Zensu CLI

A GitHub-CLI-style command-line tool for [Zensu](https://zensu.dev) — manage
products, features, and authentication from your terminal. Cross-platform
(Linux / macOS / Windows).

The CLI is a thin client over the Zensu REST API and the OAuth 2.0 + PKCE
subsystem. It works against the hosted service and any self-hosted deployment.

## Install

No Go toolchain required for any option except the last two.

### Homebrew (macOS / Linux)

```bash
brew install --cask mkitconsulting/tap/zensu
```

Taps `MKITConsulting/homebrew-tap` and installs the latest release. Upgrade with
`brew upgrade --cask zensu`. The macOS binary is signed with an Apple Developer ID
and notarized by Apple, so it runs without a Gatekeeper prompt.

### Install script (Linux / macOS)

```bash
curl -fsSL https://zensu.dev/install.sh | sh
```

Installs the latest release binary to `/usr/local/bin`. Override with
`ZENSU_INSTALL_DIR=...`, pin a version with `ZENSU_VERSION=vX.Y.Z`.

### Install script (Windows / PowerShell)

```powershell
irm https://zensu.dev/install.ps1 | iex
```

Installs the latest release to `%LOCALAPPDATA%\Programs\zensu` and adds it to your
user `PATH` (restart the terminal afterwards). Override with `$env:ZENSU_INSTALL_DIR`,
pin a version with `$env:ZENSU_VERSION`. Only `amd64` is published; on Windows arm64
it installs the amd64 build, which runs under emulation.

### Prebuilt binaries

Download the archive for your OS/arch from the
[releases](https://github.com/MKITConsulting/zensu-cli/releases), extract, and put
`zensu` on your `PATH`. Each release ships `tar.gz` (Linux/macOS), `zip`
(Windows), and a `..._checksums.txt`.

### Docker

```bash
docker run --rm ghcr.io/mkitconsulting/zensu-cli:latest --help
```

### Go toolchain

```bash
go install github.com/MKITConsulting/zensu-cli/cmd/zensu@latest
```

### From source

```bash
make build        # -> bin/zensu
make install      # -> $GOBIN/zensu
```

## Updates

To update, re-run whichever method installed `zensu` — each one overwrites the
binary in place with the latest release:

```bash
curl -fsSL https://zensu.dev/install.sh | sh        # Linux / macOS
irm https://zensu.dev/install.ps1 | iex             # Windows / PowerShell
go install github.com/MKITConsulting/zensu-cli/cmd/zensu@latest
docker pull ghcr.io/mkitconsulting/zensu-cli:latest
```

`zensu` checks GitHub for a newer release at most once every 24 hours and, when
one exists, prints a one-line hint to stderr after your command finishes. The
check is cached, never blocks the command, and is automatically skipped in
non-interactive contexts (pipes, redirects, CI). Opt out entirely by setting
`ZENSU_NO_UPDATE_CHECK` (or the conventional `NO_UPDATE_NOTIFIER`).

## Authentication

```bash
# Browser login (OAuth2 + PKCE, opens your browser)
zensu auth login

# Non-interactive / CI: log in with an API key
zensu auth login --with-token zsk_xxx
echo "$ZENSU_API_KEY" | zensu auth login --with-token -

zensu auth status      # who am I, token expiry
zensu auth token       # print the token for scripting
zensu auth logout
```

Credentials are stored in `hosts.json` under the config dir (resolved as
`$ZENSU_CONFIG_DIR`, else `$XDG_CONFIG_HOME/zensu`, else `~/.config/zensu`) with
`0600` permissions.

Every write to `hosts.json` — a token refresh, `auth login`, `auth logout` and the identity
backfill of `auth status` — holds the lock file `hosts.json.lock` beside it. Parallel zensu
processes, such as several agents or sessions, therefore rotate the refresh token once and adopt
the stored result. Presenting an already rotated refresh token again makes the server revoke the
whole token family, which would sign out every process. A process whose stored login was signed
out or moved to another host in the meantime stops with an error instead of writing it back.

### Self-hosted

Point the CLI at any Zensu deployment:

```bash
zensu --api-url https://zensu.internal.example.com products list
# or
export ZENSU_API_URL=https://zensu.internal.example.com
```

OAuth endpoints are discovered via `/.well-known/oauth-authorization-server`
(falling back to `/oauth/authorize` + `/oauth/token`), only when a login or a token refresh
needs them.

## Commands

Commands are grouped by resource. Run `zensu <group> --help` for the subcommands
and flags of each group.

| Group | Manages |
| --- | --- |
| `auth` | Authenticate `zensu` with a Zensu host (see [Authentication](#authentication)) |
| `products` | Products |
| `features` | Features |
| `subfeatures` | Sub-features of a feature |
| `tiers` | Pricing tiers and per-feature tier availability |
| `roadmap` | Product roadmaps |
| `journeys` | User journeys |
| `security` | Feature security classification, tests, reviews, and posture |
| `ghost` | Ghost scans and feature candidates |
| `pulse` | Development sessions |
| `ai-work` | Authenticated work declarations and result submissions; human acceptance stays in the web UI |
| `work` | Work orders for autonomous delivery: dispatch, claim, session reporting, questions, decisions |
| `plan` | Work plans: push plan files, follow their status, approve, finalize, abandon |
| `link` | Link tests, docs, and source files to a feature |
| `knowledge` | Load feature context packages, record agent/source retrievals, search passages and inspect knowledge items |
| `design` | Inspect a product's design system |
| `mocks` | Upload and inspect a feature's design mocks |
| `wiki` | Wiki pages |
| `org` | Inspect the organization |
| `doc` | Generate documentation context and CLAUDE.md templates |
| `meta` | Agent integration and workflow guidance helpers |
| `completion` | Shell completion scripts (see [Shell completion](#shell-completion)) |

Context retrieval receipts are explicit: `zensu knowledge context-pack ZEN-42 --json --agent codex`
returns a `load_id`. Pass it to `zensu knowledge get <item-id> --json --context-load <load-id>`
for relevant source reads. Use the actual host label (`codex`, `claude-code`, `copilot`,
`kiro`, `antigravity`, `gemini-cli` or `cli`). The feature context board groups these
server-processed retrievals by package, authenticated account and server time. The
agent label is client-declared. A receipt does not prove model comprehension, and
local file reads are not recorded. Reads without these flags preserve the original
GET behavior. The backend must support context-load receipts; errors are never
silently replaced by untracked reads. Plugins and CLI are released separately.

Context JSON preserves the backend's relevance split: read `layers` first, then
consult `related_layers` as a separate metadata index when a specific task question
needs more evidence. Both groups retain `relevance`, `reason` and the full directed
`path`; sharing a document or container does not establish a dependency. The CLI
does not eagerly fetch either group's sources. Choose individual items and use
`knowledge get --context-load` with the same receipt. If `truncated` is true, use
targeted `knowledge search --query "<question>" --json` and correlated item reads;
increasing depth does not prove completeness. Older backends without `related_layers`
return unseparated context and require an explicit relevance assessment by the agent.

Pulse treats the server-side privacy setting as authoritative. When tracking is
disabled, `zensu pulse start` and `zensu pulse end` succeed as no-ops and explain
that nothing was recorded. With `--json`, they preserve the machine-readable
`{"status":"tracking_disabled"}` response so integrations can skip follow-up
commands when no session id was created. Agent integrations should use
`--minimal-json`, which returns only `id` or `status` and omits user, organization,
project-path, changed-file, and feature metadata. Failures in minimal mode use
stable local messages and never reflect remote response data. Pulse session
arguments and enabled responses must contain canonical UUIDs; `pulse end` also
requires the response id to match the requested session. Pass changed paths with
one `--changed-file <path>` per file so commas and surrounding whitespace remain
part of the filename; the legacy comma-separated `--changed-files` flag remains
available for compatibility.

`zensu plan push <file>` drafts a work plan from a Markdown plan file. Before its first write it
refuses a revision that is an open item of another live plan, naming the item, the revision and
that plan (`zensu plan abandon <plan id>` frees it), and new features beyond the organization's
feature allowance; `--dry-run` reports the same refusals. `task` links the work order of a
one-item plan, and `scope_summary` belongs to new features and to `new_revision: true` items.
`zensu plan status <plan id> --watch` retries timeouts, refused or reset connections, responses
cut off mid-body, 429 and 5xx with backoff, prints one stderr line per retry and stops after five
failed polls in a row; TLS failures, refused redirects and an invalid API URL end it at once. An
agent key claims only in products whose automation policy allows it:
`zensu work policy set --product <product id> --add-allowed-key <key id>` adds one,
`--remove-allowed-key` takes one off and `--allowed-key` replaces the list. The command writes
the policy back with the version it read; when someone changed it in between, it reads the
policy again and repeats the change. It makes at most three attempts and notes each repeat on
stderr. A Zensu server from before this check refuses the version; the command then writes once
without it and says on stderr that a simultaneous change is not detected. Only an admin or owner
may change the allowed keys: when a member's key flags change the list, the server answers
`403 agent_keys_admin_only` and the error says that an admin or owner must change the keys.

`zensu work list` takes `--status` more than once or as a comma-separated list
(`--status blocked,queued`) and `--sort created|queue|blocked`; `queue` is the claim order and
`blocked` orders by the time an order blocked. `zensu work events <order id>` lists the events
oldest first, in the order the server stored them. With `--newest-first` it lists the newest
events first, `--per-page` at a time, and its last line names the `--before <seq>` value that
shows the next older slice. A server that does not support this order yet makes the command
fail instead of showing the events oldest first.

Both merge confirmations name the merged head. `zensu work confirm-merge <order id> --head-sha
<sha>` must match the head the order recorded, or the server answers `409 head_mismatch`; the
error points to `--acknowledge-head-mismatch`, which confirms the merge at the other head and
records that decision on the order. `zensu plan confirm-merge <plan id> --head-sha <sha>` must
match the final PR head the plan recorded. When the final PR moved on, record it again with
`zensu plan finalize <plan id> --pr-url <url> --pr-number <n> --head-sha <sha>` and then
confirm the merge. A Zensu server from before the plan head check refuses the head; the command
then confirms once without it and says so on stderr.

### Secure Wiki images

```sh
zensu wiki images policy --product "$PRODUCT_ID"
zensu wiki images upload --product "$PRODUCT_ID" --file ./dashboard.png --json
```

The upload accepts a regular local PNG/JPEG/WebP file, reads the server limits,
and sends its bytes for sanitization. It never fetches an image URL. The result
contains `reference=wiki-asset:<uuid>`, dimensions, byte size and MIME type.
Embed that reference in a page in the same product:
`![Weekly totals](wiki-asset:<uuid> "Dashboard"){width=50%}`.
Omit the suffix for automatic sizing; use integer 1–100% or 1–4096px otherwise.
Height stays proportional and width fits the current content column. This is a
Zensu Markdown extension; other renderers may show the suffix literally.

Uploads are private and do not publish pages. Use the same author for upload and
first save, keep references in the same product, and explicitly save the page.
Unreferenced uploads expire after 30 days; saved pages/revisions retain them.
Use meaningful alt text, empty alt only for decorative images, and escape
Markdown metacharacters. Never invent UUIDs or embed remote/HTML/data/blob images.
`zensu doc gen-context` carries `markdownGuidance` with these authoring rules.

This command requires the backend release adding `/api/wiki/images/{productID}`;
release that backend before this CLI. It uses the existing configurable API URL,
normal read/write scopes and BlobStore, including self-hosted storage. A 413
requires a smaller file; 429 is retryable. Uploads are bounded to 10 MiB and the
lower server policy. Pixel/format validation remains authoritative on the server.

`zensu wiki create` creates new pages only and never changes an existing one. The server derives
the new page's path from the title, or for a feature-linked page from the feature and the doc
type, for example `/custom/<slug of the title>` or `/features/<feature slug>/<doc type>`. When a
page already exists at that path, the server answers `409` with the code `PAGE_EXISTS` and leaves
the page unchanged, and the command fails with
`error: a wiki page already exists at <path> (<page id>) and was left unchanged; use "zensu wiki update <page id>" to change it (status 409)`.
With `--json` the error line carries `status` 409 and no `code`, because `PAGE_EXISTS` does not
match the code pattern described under [Errors](#errors). `zensu wiki update <page id>` changes an
existing page. The check needs Zensu backend v0.89.2 and wiki v0.15.1 or later; an older server
ignores it, updates the existing page and the command still reports a created page.

A typical `products` / `features` flow:

```bash
zensu products list
zensu products get <product-id>
zensu products create --name "My Product" --type public   # public | internal | hybrid

zensu features list --product <product-id> [--status testing]
zensu features get <feature-id>
zensu features create --product <product-id> --component <component-id> --title "Login" [--slug login]
zensu features update <feature-id> --title "New title" [--description ... --priority ...]
zensu features status <feature-id> testing
```

`--slug` is derived from `--title` when omitted. Every command that returns data
accepts `--json` for raw JSON output; on commands that print JSON anyway, such as
`zensu features get`, it only adds the JSON error line described under
[Errors](#errors). The `auth`, `meta` and `completion` commands and the commands
whose API call returns no data, such as `zensu roadmap delete`, have no `--json`.

The paginated list commands (`products list`, `features list`,
`subfeatures list`, `journeys list`, `journeys steps`, `tiers list`,
`roadmap list`, `mocks list`, `wiki list`) read every page, 100 items per
request, so the table and the `--json` output are not cut off after the first
page. With `--json` they print one envelope for the whole list,
`{"data": [...], "total": N, "page": 1, "perPage": N}`, with all N items in
`data`. `features list --status` filters on the status shown in the STATUS
column. The CLI compares the number of items it received with the server's
total. If it received fewer, because pages overlapped or shifted while they
were read, it reads the list once more; if the second read is still short, the
command fails instead of printing a partial list. A change made while the list
is read can still go unnoticed, for example when one item is deleted and
another one is added at the same time. With `--json`, the other list commands
print the server's plain JSON array. `ghost candidates` reads every page as
well, 100 candidates per request, and prints all candidates of the scan as one
plain JSON array, ordered by confidence. An older server answers
`ghost candidates` with a plain JSON array instead of the envelope. The command
then asks it for up to 200 candidates, the most a scan can hold, and prints
that array unchanged.
An older server answers `wiki list` with a plain JSON array of at most 50
pages instead of the envelope. The command then prints the table as before, or
that array unchanged with `--json`, and warns on stderr when the array holds
exactly 50 pages, because later pages may be missing.

## Errors

A failed command exits with status `1` and reports the failure on stderr. Scripts and agent
harnesses can rely on the format below.

The first line is `error: <message>`. When the Zensu API refuses a request, the message ends
with the HTTP status and the API's error code:

```text
error: the order is held by another attempt or its lease expired; stop without further forge writes (status 409, code stale_attempt)
```

- Without a code the suffix is `(status 409)`. The CLI prints a code only when it matches
  `^[a-z][a-z0-9_]{0,63}$` and treats any other value as absent.
- A response without a JSON `message` prints `error: request failed (status 502): <body excerpt>`,
  under the same suffix rule: `error: request failed (status 409, code stale_attempt): <body excerpt>`.
- A command can add context around the API's message, for example
  `error: item 3: create feature "Checkout": feature with this slug already exists (status 409, code conflict); created before the failure: …`.
- The message can span several lines.

With `--json`, stderr ends with exactly one more line, written after the `error:` line:

```text
{"error":{"status":409,"code":"stale_attempt","message":"the order is held by another attempt or its lease expired; stop without further forge writes"}}
```

`message` is the error text without the status suffix, including any context the command added.
`status` and `code` are left out when they are absent, so a network, flag or validation error
carries only `message`. The line follows the `--json` flag, not a command's default output:
commands that print JSON by default, such as `zensu work get` and `zensu features get`, add the
JSON error line only with `--json`. A flag
error gets the line too when `--json` is on the command line, even after the flag that failed.
Prefer this line for machine reading, because the message in the `error:` line comes from the
server.

The update notice (see [Updates](#updates)) never appears when stderr is not a terminal. In a
terminal it is printed before the `error:` line, so the JSON line always stays last.

## Shell completion

`zensu` generates completion scripts via `zensu completion <bash|zsh|fish|powershell>`.
Each shell needs its completion system enabled *before* the script is installed —
on macOS the default zsh ships with completion **disabled**, which is the usual
reason `zensu <TAB>` does nothing.

**zsh** — do all three steps (step 1 is the one most setups are missing):

```zsh
# 1) enable completion once (skip if ~/.zshrc already calls compinit)
echo 'autoload -Uz compinit; compinit' >> ~/.zshrc

# 2) install the completion
zensu completion zsh > "$(brew --prefix)/share/zsh/site-functions/_zensu"

# 3) restart the shell
exec zsh
```

Completions still missing? Clear the stale cache: `rm -f ~/.zcompdump*; exec zsh`.

**bash** (needs the `bash-completion` package):

```bash
echo 'source <(zensu completion bash)' >> ~/.bashrc
```

**fish**:

```fish
zensu completion fish > ~/.config/fish/completions/zensu.fish
```

**PowerShell** (Windows) — append to your profile:

```powershell
zensu completion powershell >> $PROFILE
```

Run `zensu completion <shell> --help` for the full per-shell instructions.

## Agent skill

[`skills/zensu/`](skills/zensu/) contains a standalone agent skill that teaches
AI coding agents how to drive Zensu through this CLI — feature tracking, status
transitions, security reviews, release gates, ghost scans, pulse sessions, and
doc generation. It is plain Markdown in the `SKILL.md` format: it requires only
the `zensu` binary, no agent plugin, and works with any agent that loads
skills (Claude Code, and any other host that supports the format).

```bash
# Claude Code (personal skills)
mkdir -p ~/.claude/skills
cp -r skills/zensu ~/.claude/skills/zensu

# or per project
mkdir -p .claude/skills
cp -r skills/zensu .claude/skills/zensu
```

If you use Claude Code with the full
[zensu-claude-code](https://github.com/MKITConsulting/zensu-claude-code)
plugin, skip this — the plugin ships richer `/zensu:*` skills, agents, and
hooks.

## Configuration precedence

API base URL: `--api-url` flag → `ZENSU_API_URL` → stored host → `https://api.zensu.dev`.

Work order sessions: `ZENSU_SESSION_TOKEN` holds the order-scoped `zst_…` token of a claim. When
it is set, every command authenticates with it and ignores the stored login. The API base URL
resolves as above but must be explicit: a session token is never sent to the built-in default
host, so set `--api-url`, `ZENSU_API_URL` or a stored host. Inside a session `zensu auth token`
refuses and `zensu auth status` reports the session.

Human gates: drafting (`zensu plan push`, `zensu work create`) needs a signed-in person, and every
decision needs the browser login of `zensu auth login`: `zensu work
approve|requeue|cancel|confirm-merge|answer|overturn`, `zensu work policy set`, `zensu work
repositories add|update|remove`, `zensu plan approve|finalize|abandon|confirm-merge` and
`zensu plan followup accept|dismiss`. API keys, agent keys and MCP clients get `403`. A CLI login
from before the release that added the `azp` claim is refused at decisions until you run
`zensu auth login` again.

OAuth endpoint discovery: the CLI reads `/.well-known/oauth-authorization-server` from the API
host and, by default, honours the endpoints it names only when they sit on that same host —
port and case normalised. The token endpoint receives your refresh token, so a discovery
document must not be able to move it to a host you never named. Redirects during discovery and
during the login exchange are refused for the same reason.

Separate auth host: `ZENSU_TRUSTED_AUTH_HOSTS` is a comma-separated list of additional hosts
whose discovered endpoints are accepted, for a deployment whose issuer really is a different
host. Naming a host here does not waive the HTTPS requirement — an `https` API URL still
refuses an `http` endpoint. Unset means no host beyond the API host is trusted.

Mock upload size: `ZENSU_MAX_UPLOAD_BYTES` → `33554432` (32 MiB). Raise it for a deployment
whose server accepts larger design mocks. The value is capped at 536870912 (512 MiB), because
the CLI assembles the whole multipart body in memory before sending it. An unparseable or
non-positive value falls back to the 32 MiB default.

Response size: `ZENSU_MAX_RESPONSE_BYTES` → `67108864` (64 MiB), capped at 536870912 (512 MiB).
This bounds every API response read through a `zensu` subcommand — not only `mocks get --raw` —
so a peer cannot drive the process out of memory by streaming. It does not reach the OAuth
token and endpoint-discovery requests, which read their own responses. It is deliberately
separate from the upload knob: raising what you may upload should not raise how much a server
may make you hold.

## License

[Apache License 2.0](LICENSE).


### Work capture metadata

`zensu ai-work start --id <uuid> --title <objective>` declares source `cli`, the
installed CLI version and run kind `work`. Explicit validation/demo/probe goals
use `--run-kind test`; implementing tests for a product remains normal work.
Plugins supply `--source` (`codex`, `claude_code`, `antigravity`, `copilot`, `kiro`)
and `--source-version` from their package. These are client declarations, not
verified host identity. Keep source/version/purpose stable when retrying a start.
Update the backend first; old servers reject the metadata fields visibly.
Historical plugin receipts use the hidden `--legacy-metadata` compatibility flag
to replay unchanged declarations without inventing origin or purpose. It cannot
be combined with metadata flags. Classification corrections and acceptance stay
in the authenticated web UI. Tests are excluded from default analytics; all
booked credits remain separately unfiltered because there is no run-cost linkage.

### Task-description context discovery

`zensu knowledge context-search --query "Fix account login" --json` returns up to five distinct feature candidates with canonical feature/product IDs and bounded excerpts. It performs one organization-scoped search and at most five feature-item reads, creates no context receipt, and never selects by search score. The agent matches the task and repository/product scope, asks when ambiguous, then calls `knowledge context-pack <feature-id> --agent <host>` and correlates required source reads with `--context-load`. Optional `--product <UUID>` filters the bounded candidates; no matches do not prove absence. The query is sent to the configured backend; its existing semantic search/full-text fallback is used without answer generation.
