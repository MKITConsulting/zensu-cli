---
name: zensu
description: >
  Work with Zensu — the product lifecycle manager where features are
  first-class citizens — from any coding agent or plain shell via the typed
  `zensu` CLI. Covers product and feature tracking, status transitions,
  security classification and reviews, release readiness, brownfield imports
  (ghost scans), development-session tracking (pulse), linking tests, docs and
  source files to features, roadmaps, pricing tiers, user journeys, and
  documentation generation. Use when the user mentions Zensu, a KEY-N feature
  id (e.g. ZEN-42), feature tracking, release gates, or managing their product
  lifecycle from the terminal. Standalone — requires only the zensu CLI binary,
  no agent plugin. If the zensu-claude-code plugin is installed, prefer its
  /zensu:* skills instead.
license: Apache-2.0
---

# Working with Zensu via the CLI

Zensu is a product lifecycle manager. The hierarchy is: organization → products
→ components → features → sub-features. A feature is a first-class record with a
status, a security profile, linked tests / docs / source files, tier
availability, and journey membership. Features are referenced by `KEY-N` ids
(for example `ZEN-42`) — the id doubles as the feature slug.

Feature statuses: `planned` → `in-progress` → `testing` → `released`
(deprecation is a separate command, not a status transition). The API enforces
release gates: a feature cannot move to `released` until its security and
documentation requirements are satisfied.

The `zensu` CLI is a thin, typed client over the Zensu REST API. It works
against the hosted service and any self-hosted deployment.

If your host also lists `/zensu:*` skills (the zensu-claude-code plugin is
installed), use those instead of this skill — they add agents, hooks, and
review chains on top of the same CLI.

## Session start: verify the CLI

```bash
zensu -v            # installed?
zensu auth status   # authenticated? against which host?
```

- **Not installed** → macOS/Linux: `brew install --cask mkitconsulting/tap/zensu`
  or `curl -fsSL https://zensu.dev/install.sh | sh`. Windows:
  `irm https://zensu.dev/install.ps1 | iex`. More options in
  [reference.md](reference.md).
- **Not authenticated** → run `zensu auth login` (browser OAuth2 + PKCE). In
  CI / headless environments use an API key:
  `echo "$ZENSU_API_KEY" | zensu auth login --with-token -`
- **Self-hosted** → point the CLI at the deployment with
  `export ZENSU_API_URL=https://zensu.internal.example.com` or the global
  `--api-url` flag.
- **Any auth error mid-session** (401, invalid_grant, expired token) → run
  `zensu auth login` and retry the command.

## Ground rules

1. **The CLI provides data; you do the reasoning.** Commands return structured
   context. Analyze, recommend, and decide yourself — never expect the CLI to
   make product decisions.
2. **Use `--json` whenever you parse output.** Every typed command supports it.
3. **Never guess ids.** Resolve product, component, and feature ids with
   `list`/`get` commands or ask the user.
4. **Status changes go through the dedicated command** —
   `zensu features status <id> <new-status>` — never `zensu features update`.
5. **Security classification comes before implementation.** Check or set the
   classification (`zensu security classify`) when work on a feature starts,
   not at release time.
6. **Reference features in commits** as `[KEY-N]`, e.g. `[ZEN-42]`.
7. **Enrich, don't duplicate.** When applying a ghost scan to a product that
   already has features, use `zensu ghost apply --enrich-existing`.
8. **Confirm destructive or bulk actions with the user first** (ghost apply,
   feature merge/split/deprecate, roadmap delete).
9. **Help output is authoritative.** For any flag detail not listed here, run
   `zensu <group> [<command>] --help`.

## Orientation

First commands in any session, in order of usefulness:

```bash
zensu products list --json                                  # products + ids
zensu products get <product-id> --json                      # detail incl. components[]
zensu features list --product <product-id> --json           # add --status planned|in-progress|testing|released
zensu features get <feature-id> --json                      # full feature detail
zensu features history <feature-id> --json                  # timeline of one feature
zensu knowledge search --query "<topic>" --json             # what the org already knows
```

## Core workflows

### Track new work

Component ids come from `zensu products get <product-id> --json` (the response
embeds `components`).

```bash
zensu features create --product <id> --component <id> --title "Login" \
  [--slug login] [--status planned] --json
zensu subfeatures add <feature-id> --title "Password reset" \
  [--priority critical|high|medium|low] [--status planned]
```

### Implement a tracked feature (KEY-N)

```bash
zensu features get <feature-id> --json                  # 1. load full context
zensu security classify <feature-id> --classification internal ...   # 2. classify early
zensu features status <feature-id> in-progress          # 3. start work
# ... implement in the repository ...
zensu link test <feature-id> --file src/auth/login.test.ts \
  --test-type unit --last-run-status passed             # 4. link artifacts
zensu link source <feature-id> --file src/auth/login.ts
zensu link docs <feature-id> --doc-type user_facing --content "..."
zensu features status <feature-id> testing              # 5. hand to testing
zensu security validate <feature-id> --json             # 6. release gates green?
zensu features status <feature-id> released             # 7. only after gates pass
```

### Security review

```bash
zensu security classify <feature-id> --classification confidential \
  --data-sensitivity pii --auth-required --auth-type jwt ...
zensu security suggest-tests <feature-id> --json    # context for picking tests
zensu security add-test <feature-id> --file tests/security/login_test.go \
  --type injection [--owasp-id A03:2021] [--last-run-status passed]
zensu security review <feature-id> --reviewer <who> --status approved \
  [--type manual|automated|external] [--findings "..."]
zensu security score <feature-id> --json            # score + release-gate state
zensu security posture <product-id> --json          # product-wide overview
zensu security threat-model <feature-id> --json     # STRIDE context data
```

### Release readiness

```bash
zensu security validate <feature-id> --json   # all security requirements met?
zensu journeys health <journey-id> --json     # journey coverage healthy?
zensu security posture <product-id> --json    # aggregate view
```

### Brownfield: import an existing codebase (ghost scan)

You analyze the repository yourself and feed the discovered candidates to Zensu:

```bash
zensu ghost scan --product <id> --candidates '<json-array>' \
  [--components '<json-array>'] [--repo-url ...] [--branch ...] --json
zensu ghost candidates <scan-id> --json        # ordered by confidence
# present candidates to the user, then:
zensu ghost batch <scan-id> --product <id> \
  --approve-ids '["<uuid>", ...]' --reject-ids '["<uuid>", ...]' \
  [--reject-reason "..."]
zensu ghost apply <scan-id> --product <id> --enrich-existing
```

Run `zensu ghost scan --help` for the exact candidate JSON shape. Use
`--enrich-existing` by default — it links discovered tests, docs, and source
files to matching existing features (by slug) instead of failing on conflicts.
Omit it only on the very first scan of an empty product.

### Greenfield: bootstrap from a vision

```bash
zensu products create --name "My Product" --type public|internal|hybrid --json
zensu products vision-create ...               # store the vision
zensu products bootstrap-apply <vision-id> --json   # structured components+features payload
zensu products bootstrap-step ...              # mark post-bootstrap steps done
```

`bootstrap-apply` takes a JSON payload of components and features (with
priority `critical|high|medium|low`, effort `S|M|L|XL`, security
classification, scope, and optional subfeatures) — you derive that payload from
the vision by your own analysis. See `zensu products bootstrap-apply --help`
for the documented shape.

### Development-session tracking (pulse)

```bash
zensu pulse start --head-sha "$(git rev-parse --short HEAD)" \
  [--branch "$(git branch --show-current)"] [--product <id>] [--project "$PWD"]
# ... work ...
zensu pulse end <session-id> --changed-files "$(git diff --name-only | paste -sd, -)"
zensu pulse summary <session-id> --json
```

Sessions are idempotent per HEAD SHA — starting again with the same SHA
continues the session.

### Documentation

```bash
zensu doc gen-context <feature-id> --json      # aggregated doc-writing context
# READ THE ACTUAL SOURCE FILES before writing — never document from metadata alone
zensu link docs <feature-id> --doc-type user_facing --audience end_user \
  --content "<markdown>"                       # creates/updates the linked wiki page
zensu wiki create --product <id> --title "..." --content "<markdown>" \
  [--doc-type overview] [--entity-type feature --entity-id <uuid>]
zensu doc claude-md --product <id> --variant full|minimal|ci-only   # CLAUDE.md template
```

## Scripting patterns

```bash
# ids for scripting
zensu features list --product "$PRODUCT" --status testing --json | jq -r '.[].id'

# raw API access when no typed command exists (prefer typed commands)
curl -sH "Authorization: Bearer $(zensu auth token)" "$ZENSU_API_URL/api/..."
```

## Full command map

Nineteen command groups: `auth products features subfeatures tiers roadmap
journeys security ghost pulse link knowledge design mocks wiki org doc meta
completion`. Per-group commands, verified flag sets, and all enum vocabularies
are in [reference.md](reference.md).
