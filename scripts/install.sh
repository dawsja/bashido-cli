#!/bin/sh
set -eu

repo='dawsja/bashido-cli'
version='latest'
bin_dir=''
server=${BASHIDO_SERVER:-}
profile='default'
configure=1

usage() {
  printf '%s\n' 'Usage: install.sh [--version VERSION] [--bin-dir DIR] [--server URL] [--profile NAME] [--no-config]'
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version|--bin-dir|--server|--profile)
      [ "$#" -ge 2 ] || { usage >&2; exit 2; }
      case "$1" in
        --version) version=$2 ;;
        --bin-dir) bin_dir=$2 ;;
        --server) server=$2 ;;
        --profile) profile=$2 ;;
      esac
      shift 2 ;;
    --no-config) configure=0; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'install.sh: unknown option: %s\n' "$1" >&2; exit 2 ;;
  esac
done

[ "$(uname -s)" = Linux ] || { printf '%s\n' 'install.sh: Linux is required' >&2; exit 1; }
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) printf 'install.sh: unsupported architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

if [ -z "$bin_dir" ]; then
  if [ "$(id -u)" -eq 0 ]; then bin_dir=/usr/local/bin; else bin_dir=${HOME:?}/.local/bin; fi
fi
mkdir -p "$bin_dir"
[ -d "$bin_dir" ] && [ -w "$bin_dir" ] || { printf 'install.sh: directory is not writable: %s\n' "$bin_dir" >&2; exit 1; }

asset="bashido-linux-$arch"
if [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  case "$version" in v*) tag=$version ;; *) tag=v$version ;; esac
  base="https://github.com/$repo/releases/download/$tag"
fi

tmp_bin=$(mktemp "$bin_dir/.bashido.XXXXXX")
tmp_sum=$(mktemp "${TMPDIR:-/tmp}/bashido-checksums.XXXXXX")
trap 'rm -f "$tmp_bin" "$tmp_sum"' EXIT HUP INT TERM

download() {
  command -v curl >/dev/null 2>&1 || { printf '%s\n' 'install.sh: curl is required' >&2; exit 1; }
  curl -fL --proto '=https' --proto-redir '=https' --tlsv1.2 -o "$2" "$1"
}
download "$base/$asset" "$tmp_bin"
download "$base/checksums.txt" "$tmp_sum"

expected=''
while read -r sum name extra; do
  if [ "$name" = "$asset" ] && [ -z "${extra:-}" ]; then expected=$sum; break; fi
done < "$tmp_sum"
[ -n "$expected" ] || { printf '%s\n' "install.sh: checksum for $asset is missing" >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then actual=$(sha256sum "$tmp_bin"); actual=${actual%% *}
elif command -v shasum >/dev/null 2>&1; then actual=$(shasum -a 256 "$tmp_bin"); actual=${actual%% *}
elif command -v openssl >/dev/null 2>&1; then actual=$(openssl dgst -sha256 "$tmp_bin"); actual=${actual##* }
else printf '%s\n' 'install.sh: sha256sum, shasum, or openssl is required' >&2; exit 1
fi
[ "$actual" = "$expected" ] || { printf '%s\n' 'install.sh: checksum mismatch' >&2; exit 1; }

chmod 0755 "$tmp_bin"
mv -f "$tmp_bin" "$bin_dir/bashido"
"$bin_dir/bashido" version >/dev/null
trap - EXIT HUP INT TERM
rm -f "$tmp_sum"

if [ "$configure" -eq 1 ] && [ -n "$server" ]; then
  "$bin_dir/bashido" profile add "$profile" "$server" --use
fi
printf 'Installed bashido to %s\n' "$bin_dir/bashido"
