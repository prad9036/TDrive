#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'TDrive WSL tool setup: %s\n' "$*" >&2
  exit 1
}

for command_name in curl tar xz sha256sum awk; do
  command -v "$command_name" >/dev/null 2>&1 || fail "'$command_name' is required; install it in WSL first"
done

case "$(uname -m)" in
  aarch64|arm64)
    go_arch=arm64
    go_sha256=3f4227ce7b206a513e794f1393adb473a07b46e94d18bd15d1c684bbb7319797
    node_arch=arm64
    ;;
  x86_64|amd64)
    go_arch=amd64
    go_sha256=ccfe7ae822d2c2f41f4fe7c956199967a4dac22d0ee0d2b2bfdb440aab3b20c5
    node_arch=x64
    ;;
  *) fail "unsupported WSL architecture: $(uname -m)" ;;
esac

go_version=1.26.10
node_version=22.21.1
tool_root=${TDRIVE_TOOLCHAIN_ROOT:-${XDG_DATA_HOME:-$HOME/.local/share}/tdrive-toolchain}
node_dir="node-v${node_version}-linux-${node_arch}"
mkdir -p "$tool_root"
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/tdrive-wsl-tools.XXXXXX") || fail "could not create a temporary download directory"
cleanup() { rm -rf -- "$tmp_dir"; }
trap cleanup EXIT

if [[ ! -x $tool_root/go/bin/go ]]; then
  go_archive="go${go_version}.linux-${go_arch}.tar.gz"
  printf 'Downloading Go %s for Linux %s...\n' "$go_version" "$go_arch"
  curl -fL "https://go.dev/dl/$go_archive" -o "$tmp_dir/$go_archive"
  printf '%s  %s\n' "$go_sha256" "$tmp_dir/$go_archive" | sha256sum -c -
  mkdir "$tmp_dir/go"
  tar -xzf "$tmp_dir/$go_archive" -C "$tmp_dir/go" --strip-components=1
  [[ ! -e $tool_root/go ]] || fail "$tool_root/go exists but has no working Go binary; move it aside and retry"
  mv "$tmp_dir/go" "$tool_root/go"
fi
"$tool_root/go/bin/go" version | grep -Fq "go${go_version} " || fail "the installed Go version is not go${go_version}"

if [[ ! -x $tool_root/$node_dir/bin/node ]]; then
  node_archive="node-v${node_version}-linux-${node_arch}.tar.xz"
  printf 'Downloading Node.js %s for Linux %s...\n' "$node_version" "$node_arch"
  curl -fL "https://nodejs.org/dist/v${node_version}/$node_archive" -o "$tmp_dir/$node_archive"
  curl -fsSL "https://nodejs.org/dist/v${node_version}/SHASUMS256.txt" -o "$tmp_dir/node-shasums.txt"
  node_sha256=$(awk -v name="$node_archive" '$2 == name { print $1 }' "$tmp_dir/node-shasums.txt")
  [[ $node_sha256 =~ ^[[:xdigit:]]{64}$ ]] || fail "could not find the official SHA-256 for $node_archive"
  printf '%s  %s\n' "$node_sha256" "$tmp_dir/$node_archive" | sha256sum -c -
  mkdir "$tmp_dir/$node_dir"
  tar -xJf "$tmp_dir/$node_archive" -C "$tmp_dir/$node_dir" --strip-components=1
  [[ ! -e $tool_root/$node_dir ]] || fail "$tool_root/$node_dir exists but has no working Node binary; move it aside and retry"
  mv "$tmp_dir/$node_dir" "$tool_root/$node_dir"
fi
"$tool_root/$node_dir/bin/node" --version | grep -Fxq "v${node_version}" || fail "the installed Node.js version is not v${node_version}"

export GOPATH="$tool_root/gopath"
export GOCACHE="$tool_root/gocache"
export GOTOOLCHAIN=local
export CGO_ENABLED=0
export PATH="$tool_root/go/bin:$tool_root/$node_dir/bin:$PATH"
mkdir -p "$GOPATH/bin" "$GOCACHE"

if [[ ! -x $GOPATH/bin/wails3 ]] || ! "$GOPATH/bin/wails3" version 2>/dev/null | grep -Fq 'v3.0.0-beta.22'; then
  printf 'Installing the Wails CLI pinned by this project...\n'
  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.22
fi
"$GOPATH/bin/wails3" version 2>/dev/null | grep -Fq 'v3.0.0-beta.22' || fail "Wails CLI installation did not produce v3.0.0-beta.22"

printf 'WSL build tools are ready under %s.\n' "$tool_root"
