#!/bin/sh
# Zensu CLI installer.
#   curl -fsSL https://zensu.dev/install.sh | sh
# Env overrides: ZENSU_VERSION (default latest), ZENSU_INSTALL_DIR (default /usr/local/bin), ZENSU_RELEASE_URL, ZENSU_REQUIRE_SIGNATURE=1, ZENSU_OPENSSL.
set -eu

REPO="MKITConsulting/zensu-cli"
BIN="zensu"

release_keys() {
	echo "7hr40IQBkj9kWZmBYO/AlfOH8fk/L27k5btCyV9Ciyg="
}

selftest_key() {
	echo "PUAXw+hDiVqStwqnTRt+vJyYLM8uxJaMwM1V8Sr0Zgw="
}

selftest_signature() {
	echo "kqAJqfDUyrhyDoILX2QlQKKye1QWUD+Ps3YiI+vbadoIWsHkPhWZbkWPNhPQ8R2MOHsurrQwKu6wDSkWErsMAA=="
}

main() {
	os="$(detect_os)"
	arch="$(detect_arch)"
	version="$(resolve_version)"
	ver="${version#v}"
	release_url="${ZENSU_RELEASE_URL:-https://github.com/${REPO}/releases/download}"

	tmp="$(mktemp -d)"
	trap 'rm -rf "$tmp"' EXIT

	archive="${BIN}_${ver}_${os}_${arch}.tar.gz"
	checksums="${BIN}_${ver}_checksums.txt"
	base="${release_url%/}/${version}"
	echo "Downloading ${archive} (${version}) ..."
	fetch "${base}/${checksums}" "${tmp}/checksums.txt"
	verify_signature "$tmp" "${base}/${checksums}.sig"
	fetch "${base}/${archive}" "${tmp}/${archive}"
	verify "$tmp" "$archive"

	tar -xzf "${tmp}/${archive}" -C "$tmp"
	install_bin "${tmp}/${BIN}"
}

detect_os() {
	case "$(uname -s)" in
		Linux) echo linux ;;
		Darwin) echo darwin ;;
		*) err "unsupported OS $(uname -s); on Windows install with install.ps1 (irm https://zensu.dev/install.ps1 | iex) or download the .zip from https://github.com/${REPO}/releases" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
		x86_64|amd64) echo amd64 ;;
		aarch64|arm64) echo arm64 ;;
		*) err "unsupported architecture $(uname -m)" ;;
	esac
}

resolve_version() {
	version="${ZENSU_VERSION:-latest}"
	if [ "$version" = "latest" ]; then
		[ -z "${ZENSU_RELEASE_URL:-}" ] || err "ZENSU_RELEASE_URL needs ZENSU_VERSION, because a mirror cannot resolve the latest release"
		version="$(latest_version)"
	fi
	[ -n "$version" ] || err "could not resolve a release version"
	case "$version" in
		v*) ;;
		*) version="v${version}" ;;
	esac
	case "${version#v}" in
		''|*[!0-9A-Za-z.+-]*) err "invalid version ${version}" ;;
	esac
	echo "$version"
}

latest_version() {
	url="https://api.github.com/repos/${REPO}/releases/latest"
	if command -v curl >/dev/null 2>&1; then curl -fsSL "$url"; else wget -qO- "$url"; fi \
		| grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name" *: *"([^"]+)".*/\1/'
}

fetch() {
	fetch_quiet "$1" "$2" || err "could not download $1"
}

fetch_quiet() {
	if command -v curl >/dev/null 2>&1; then curl -fsSL "$1" -o "$2"
	elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
	else err "need curl or wget"; fi
}

verify_signature() {
	dir="$1"; url="$2"
	if ! fetch_quiet "$url" "${dir}/checksums.txt.sig" 2>/dev/null; then
		if [ "${ZENSU_VERSION:-latest}" = "latest" ]; then
			err "could not download the signature ${url##*/} of the latest release ${version}; the latest zensu release is always signed, so nothing is installed without it"
		fi
		if signed_release; then
			err "could not download the signature ${url##*/} of release ${version}; every zensu release after v0.18.0 is signed, so nothing is installed without it"
		fi
		require_signature "release ${version} carries no signature (${url##*/})"
		note "release ${version} carries no signature, so only its checksums are checked"
		return 0
	fi
	if ! openssl_bin="$(find_openssl "$dir")"; then
		require_signature "verifying the release signature needs OpenSSL 3 or later with Ed25519 support; install it or set ZENSU_OPENSSL to its path"
		note "OpenSSL 3 or later with Ed25519 support was not found, so the release signature was not checked; set ZENSU_REQUIRE_SIGNATURE=1 to require it"
		return 0
	fi
	for key in $(release_keys); do
		printf '%s\n' "-----BEGIN PUBLIC KEY-----" "MCowBQYDK2VwAyEA${key}" "-----END PUBLIC KEY-----" > "${dir}/release.pem"
		while IFS= read -r sig || [ -n "$sig" ]; do
			sig="$(printf '%s' "$sig" | tr -d ' \t\r')"
			[ -n "$sig" ] || continue
			printf '%s' "$sig" | "$openssl_bin" base64 -d -A > "${dir}/checksums.sig.bin" 2>/dev/null || continue
			if "$openssl_bin" pkeyutl -verify -pubin -inkey "${dir}/release.pem" -rawin \
				-in "${dir}/checksums.txt" -sigfile "${dir}/checksums.sig.bin" >/dev/null 2>&1; then
				echo "Verified the release signature."
				return 0
			fi
		done < "${dir}/checksums.txt.sig"
	done
	err "the signature of ${BIN}_${ver}_checksums.txt matches no zensu release key, so the download may have been tampered with"
}

require_signature() {
	case "${ZENSU_REQUIRE_SIGNATURE:-}" in
		''|0) ;;
		*) err "$1" ;;
	esac
}

signed_release() {
	case "$ver" in
		*[!0-9.]*|*.*.*.*|*.0[0-9]*) return 0 ;;
		0.18.0|0.[0-9].[0-9]*|0.1[0-7].[0-9]*) return 1 ;;
	esac
	return 0
}

find_openssl() {
	if [ -n "${ZENSU_OPENSSL:-}" ]; then
		verifies_ed25519 "$1" "$ZENSU_OPENSSL" || return 1
		echo "$ZENSU_OPENSSL"
		return 0
	fi
	for candidate in openssl /opt/homebrew/opt/openssl@3/bin/openssl /usr/local/opt/openssl@3/bin/openssl; do
		if verifies_ed25519 "$1" "$candidate"; then
			echo "$candidate"
			return 0
		fi
	done
	return 1
}

verifies_ed25519() {
	is_openssl3 "$2" || return 1
	printf '%s\n' "-----BEGIN PUBLIC KEY-----" "MCowBQYDK2VwAyEA$(selftest_key)" "-----END PUBLIC KEY-----" > "${1}/selftest.pem"
	printf 'r' > "${1}/selftest.msg"
	printf '%s' "$(selftest_signature)" | "$2" base64 -d -A > "${1}/selftest.sig" 2>/dev/null || return 1
	"$2" pkeyutl -verify -pubin -inkey "${1}/selftest.pem" -rawin -in "${1}/selftest.msg" -sigfile "${1}/selftest.sig" >/dev/null 2>&1 || return 1
	printf 's' > "${1}/selftest.msg"
	! "$2" pkeyutl -verify -pubin -inkey "${1}/selftest.pem" -rawin -in "${1}/selftest.msg" -sigfile "${1}/selftest.sig" >/dev/null 2>&1
}

is_openssl3() {
	command -v "$1" >/dev/null 2>&1 || return 1
	case "$("$1" version 2>/dev/null)" in
		"OpenSSL "[3-9].*|"OpenSSL "[1-9][0-9]*) return 0 ;;
	esac
	return 1
}

verify() {
	dir="$1"; file="$2"
	want="$(awk -v f="$file" '$2 == f || $2 == "*" f { print $1; exit }' "${dir}/checksums.txt")"
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
	mkdir -p "$dir" 2>/dev/null || true
	if [ -w "$dir" ]; then mv "$src" "${dir}/${BIN}"
	elif command -v sudo >/dev/null 2>&1 && { sudo -n true 2>/dev/null || [ -t 0 ] || [ -t 2 ]; }; then
		echo "Installing to ${dir} (sudo) ..."; sudo mv "$src" "${dir}/${BIN}"
	else
		dir="${HOME}/.local/bin"; mkdir -p "$dir"; mv "$src" "${dir}/${BIN}"
		echo "Note: ${dir} must be on your PATH."
	fi
	echo "Installed ${BIN} to ${dir}/${BIN}"
}

note() { echo "Note: $*." >&2; }

err() { echo "error: $*" >&2; exit 1; }

main "$@"
