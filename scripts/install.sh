#!/usr/bin/env bash
# Installs the latest zklens release for this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/hugantdev/zklens/main/scripts/install.sh | bash
#
# Env vars:
#   ZKLENS_VERSION      release tag to install, e.g. v1.2.0 (default: latest)
#   ZKLENS_INSTALL_DIR  where to put the binary (default: $HOME/.local/bin)

set -euo pipefail

repo="hugantdev/zklens"
install_dir="${ZKLENS_INSTALL_DIR:-$HOME/.local/bin}"

log() { printf 'zklens-install: %s\n' "$1"; }
die() {
	printf 'zklens-install: error: %s\n' "$1" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

need curl
need tar

os=$(uname -s)
case "$os" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) die "unsupported OS: $os (install with 'go install github.com/${repo}/cmd/zklens@latest' instead)" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture: $arch" ;;
esac

version="${ZKLENS_VERSION:-}"
if [ -z "$version" ]; then
	log "looking up the latest release..."
	version=$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" | grep '"tag_name"' | head -1 | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
	[ -n "$version" ] || die "could not determine the latest release; pass ZKLENS_VERSION explicitly"
fi

archive="zklens_${os}_${arch}.tar.gz"
base_url="https://github.com/${repo}/releases/download/${version}"

work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

log "downloading ${archive} (${version})..."
curl -fsSL "${base_url}/${archive}" -o "${work_dir}/${archive}" ||
	die "download failed; check that ${version} has a release asset for ${os}/${arch}"

log "verifying checksum..."
curl -fsSL "${base_url}/checksums.txt" -o "${work_dir}/checksums.txt" ||
	die "could not download checksums.txt"

(
	cd "$work_dir"
	if command -v sha256sum >/dev/null 2>&1; then
		grep " ${archive}\$" checksums.txt | sha256sum -c - >/dev/null
	elif command -v shasum >/dev/null 2>&1; then
		grep " ${archive}\$" checksums.txt | shasum -a 256 -c - >/dev/null
	else
		log "warning: no sha256sum/shasum found, skipping checksum verification"
	fi
) || die "checksum verification failed"

tar -xzf "${work_dir}/${archive}" -C "$work_dir" zklens

mkdir -p "$install_dir"
install -m 0755 "${work_dir}/zklens" "${install_dir}/zklens"

log "installed to ${install_dir}/zklens"

case ":$PATH:" in
*":$install_dir:"*) ;;
*) log "note: ${install_dir} is not on your PATH — add it, e.g. export PATH=\"${install_dir}:\$PATH\"" ;;
esac

log "run 'zklens --help' to get started"
