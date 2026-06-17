# Zensu CLI

A GitHub-CLI-style command-line tool for [Zensu](https://zensu.dev) — manage
products, features, and authentication from your terminal. Cross-platform
(Linux / macOS / Windows).

The CLI is a thin client over the Zensu REST API and the OAuth 2.0 + PKCE
subsystem. It works against the hosted service and any self-hosted deployment.

## Install

No Go toolchain required for any option except the last two.

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

### Self-hosted

Point the CLI at any Zensu deployment:

```bash
zensu --api-url https://zensu.internal.example.com products list
# or
export ZENSU_API_URL=https://zensu.internal.example.com
```

OAuth endpoints are discovered via `/.well-known/oauth-authorization-server`
(falling back to `/oauth/authorize` + `/oauth/token`).

## Commands

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

`--slug` is derived from `--title` when omitted. Add `--json` to typed commands
for raw output.

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

## Configuration precedence

API base URL: `--api-url` flag → `ZENSU_API_URL` → stored host → `https://api.zensu.dev`.

## License

[Apache License 2.0](LICENSE).
