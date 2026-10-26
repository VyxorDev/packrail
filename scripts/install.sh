#!/bin/sh
set -eu

repo='VyxorDev/packrail'
version="${PACKRAIL_INSTALL_VERSION:-}"
if [ -z "$version" ]; then
  # This fallback is for running the source script from the repository.
  # Published release assets inject PACKRAIL_INSTALL_VERSION so they never
  # depend on the moving main branch.
  version_url='https://raw.githubusercontent.com/VyxorDev/packrail/main/VERSION'
  version="$(curl -fsSL "$version_url?cachebust=$(date +%s)" | tr -d '\r\n')"
fi
[ -n "$version" ] || { echo 'Packrail VERSION is empty.' >&2; exit 1; }

os_raw="$(uname -s)"
arch_raw="$(uname -m)"
case "$os_raw" in
  Linux) os='linux' ;;
  Darwin) os='macos' ;;
  *) echo "Unsupported OS: $os_raw" >&2; exit 1 ;;
esac
case "$arch_raw" in
  x86_64|amd64) arch='amd64' ;;
  arm64|aarch64) arch='arm64' ;;
  *) echo "Unsupported architecture: $arch_raw" >&2; exit 1 ;;
esac

asset="prl-$os-$arch"
tag="v$version"
base="https://github.com/$repo/releases/download/$tag"
tmp="$(mktemp -d 2>/dev/null || mktemp -d -t packrail-install)"
trap 'rm -rf "$tmp"' EXIT INT TERM

curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/SHA256SUMS.txt" -o "$tmp/SHA256SUMS.txt"
expected="$(awk -v a="$asset" '$2 == a || $2 == "*" a { print $1; exit }' "$tmp/SHA256SUMS.txt")"
[ -n "$expected" ] || { echo "Checksum for $asset was not found." >&2; exit 1; }

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')"
else
  echo 'Need sha256sum or shasum to verify Packrail.' >&2
  exit 1
fi

[ "$actual" = "$expected" ] || { echo "SHA-256 mismatch for $asset." >&2; exit 1; }

install_dir="${PACKRAIL_INSTALL_DIR:-$HOME/.local/bin}"
mkdir -p "$install_dir"
cp "$tmp/$asset" "$install_dir/prl"
chmod 755 "$install_dir/prl"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    profile="$HOME/.profile"
    line="export PATH=\"$install_dir:\$PATH\""
    if [ ! -f "$profile" ] || ! grep -Fq "$line" "$profile"; then
      printf '\n%s\n' "$line" >> "$profile"
    fi
    export PATH="$install_dir:$PATH"
    ;;
esac

printf 'Installed Packrail %s to %s\n' "$version" "$install_dir/prl"
printf 'Run: prl version\n'
