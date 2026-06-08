#!/bin/sh
# Zensu CLI installer.
#   curl -fsSL https://zensu.dev/install.sh | sh
# Env overrides: ZENSU_VERSION (default latest), ZENSU_INSTALL_DIR (default /usr/local/bin).
set -eu

REPO="MKITConsulting/zensu-cli"
BIN="zensu"

main() {
	os="$(detect_os)"
	arch="$(detect_arch)"
	version="${ZENSU_VERSION:-latest}"
	[ "$version" = "latest" ] && version="$(latest_version)"
	[ -n "$version" ] || err "could not resolve a release version"
	ver="${version#v}"

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT

	archive="${BIN}_${ver}_${os}_${arch}.tar.gz"
	base="https://github.com/${REPO}/releases/download/${version}"
	echo "Downloading ${archive} (${version}) ..."
	fetch "${base}/${archive}" "${tmp}/${archive}"
	fetch "${base}/${BIN}_${ver}_checksums.txt" "${tmp}/checksums.txt"
	verify "$tmp" "$archive"

	tar -xzf "${tmp}/${archive}" -C "$tmp"
	install_bin "${tmp}/${BIN}"
}

detect_os() {
	case "$(uname -s)" in
		Linux) echo linux ;;
		Darwin) echo darwin ;;
		*) err "unsupported OS $(uname -s); download the Windows .zip from https://github.com/${REPO}/releases" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
		x86_64|amd64) echo amd64 ;;
		aarch64|arm64) echo arm64 ;;
		*) err "unsupported architecture $(uname -m)" ;;
	esac
}

latest_version() {
	url="https://api.github.com/repos/${REPO}/releases/latest"
	if command -v curl >/dev/null 2>&1; then curl -fsSL "$url"; else wget -qO- "$url"; fi \
		| grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name" *: *"([^"]+)".*/\1/'
}

fetch() {
	if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
	else err "need curl or wget"; fi
}

verify() {
	dir="$1"; file="$2"
	want="$(grep " ${file}\$" "${dir}/checksums.txt" | awk '{print $1}')"
	[ -n "$want" ] || err "no checksum entry for ${file}"
	if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "${dir}/${file}" | awk '{print $1}')"
	elif command -v shasum >/dev/null 2>&1; then got="$(shasum -a 256 "${dir}/${file}" | awk '{print $1}')"
	else err "need sha256sum or shasum"; fi
	[ "$want" = "$got" ] || err "checksum mismatch for ${file}"
}

install_bin() {
	src="$1"
	chmod +x "$src"
	dir="${ZENSU_INSTALL_DIR:-/usr/local/bin}"
	if [ -w "$dir" ]; then mv "$src" "${dir}/${BIN}"
	elif command -v sudo >/dev/null 2>&1; then
		echo "Installing to ${dir} (sudo) ..."; sudo mv "$src" "${dir}/${BIN}"
	else
		dir="${HOME}/.local/bin"; mkdir -p "$dir"; mv "$src" "${dir}/${BIN}"
		echo "Note: ${dir} must be on your PATH."
	fi
	echo "Installed ${BIN} to ${dir}/${BIN}"
}

err() { echo "error: $*" >&2; exit 1; }

main "$@"
