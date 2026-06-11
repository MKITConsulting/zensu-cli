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

# Escape hatch: call any Zensu API endpoint
zensu api /api/products
zensu api POST /api/products -f name=Acme -f productType=public
zensu api PATCH /api/features/<id>/status -f status=released
```

`--slug` is derived from `--title` when omitted. Add `--json` to typed commands
for raw output.

## Configuration precedence

API base URL: `--api-url` flag → `ZENSU_API_URL` → stored host → `https://api.zensu.dev`.

## License

[Apache License 2.0](LICENSE).
