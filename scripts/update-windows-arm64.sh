#!/usr/bin/env bash
set -Eeuo pipefail

usage() {
  cat <<'EOF'
Usage: update-windows-arm64.sh 'C:\Users\prade\AppData\Local\Programs\TDrive\TDrive.exe'

Fetches the configured Git upstream, builds its source in a temporary checkout
when the remote has new commits, then replaces the specified installed app.
EOF
}

fail() {
  printf 'TDrive updater: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 1 ]] || { usage >&2; exit 2; }
target_input=$1

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(git -C "$script_dir" rev-parse --show-toplevel) || fail "could not locate the TDrive Git checkout"
cd "$repo_root"

branch=$(git symbolic-ref --quiet --short HEAD) || fail "checkout is detached; switch to a branch with an upstream first"
remote=$(git config --get "branch.$branch.remote") || fail "branch '$branch' has no configured remote"
merge_ref=$(git config --get "branch.$branch.merge") || fail "branch '$branch' has no configured upstream branch"
[[ $remote != "." && $merge_ref == refs/heads/* ]] || fail "branch '$branch' must track a remote branch"
remote_branch=${merge_ref#refs/heads/}
upstream_ref="$remote/$remote_branch"

printf 'Fetching %s/%s...\n' "$remote" "$remote_branch"
git fetch --prune "$remote"
git rev-parse --verify "$upstream_ref^{commit}" >/dev/null 2>&1 || fail "upstream '$upstream_ref' was not found after fetch"
desired_commit=$(git rev-parse "$upstream_ref")
target=$(wslpath -u "$target_input") || fail "could not convert the target path '$target_input' for WSL"
[[ -f $target && ! -L $target ]] || fail "target must be an existing regular executable: $target_input"
[[ ${target##*/} == "TDrive.exe" ]] || fail "target must be named TDrive.exe"
command -v file >/dev/null 2>&1 || fail "the 'file' utility is required to distinguish the app from its installer"
target_type=$(file -b "$target")
[[ $target_type == *"PE32+"* && $target_type == *"ARM64"* ]] || fail "this is not the Windows ARM64 TDrive app executable; pass the installed TDrive.exe, not the installer"

artifact="$repo_root/build/bin/TDrive.exe"
[[ $(realpath -m "$target") != $(realpath -m "$artifact") ]] || fail "target points to the build output, not an installed app"

target_marker="$target.tdrive-commit"
installed_commit=
if [[ -f $target_marker ]]; then
  IFS= read -r installed_commit < "$target_marker" || true
fi
if [[ $installed_commit == "$desired_commit" ]]; then
  printf 'Installed TDrive already matches commit %s.\n' "${desired_commit:0:12}"
  exit 0
fi

# Keep the toolchain across WSL restarts. The setup helper installs into this
# user-owned directory and is run automatically the first time if needed.
tool_root=${TDRIVE_TOOLCHAIN_ROOT:-${XDG_DATA_HOME:-$HOME/.local/share}/tdrive-toolchain}
case "$(uname -m)" in
  aarch64|arm64) node_dir=node-v22.21.1-linux-arm64 ;;
  x86_64|amd64) node_dir=node-v22.21.1-linux-x64 ;;
  *) fail "unsupported WSL architecture: $(uname -m)" ;;
esac
if [[ ! -x $tool_root/go/bin/go || ! -x $tool_root/gopath/bin/wails3 || ! -x $tool_root/$node_dir/bin/node ]]; then
  bash "$repo_root/scripts/setup-wsl-update-tools.sh"
fi
export PATH="$tool_root/go/bin:$tool_root/gopath/bin:$tool_root/$node_dir/bin:$PATH"
export GOPATH=${GOPATH:-$tool_root/gopath}
export GOCACHE=${GOCACHE:-$tool_root/gocache}
export GOTOOLCHAIN=local
mkdir -p "$GOCACHE"

command -v go >/dev/null 2>&1 || fail "Go setup did not complete"
command -v npm >/dev/null 2>&1 || fail "npm is missing from the Node.js toolchain"
command -v wails3 >/dev/null 2>&1 || fail "Wails CLI setup did not complete"

source_dir=$(mktemp -d "${TMPDIR:-/tmp}/tdrive-update.XXXXXX") || fail "could not create a temporary source directory"
target_stage=
marker_stage=
cleanup() {
  rm -f -- "${target_stage:-}" "${marker_stage:-}"
  rm -rf -- "${source_dir:-}"
}
trap cleanup EXIT

printf 'Preparing remote source at commit %s...\n' "${desired_commit:0:12}"
git archive "$desired_commit" | tar -x -C "$source_dir"

printf 'Building Windows ARM64 TDrive at commit %s...\n' "${desired_commit:0:12}"
(
  cd "$source_dir"
  wails3 task windows:build ARCH=arm64
)

artifact="$source_dir/build/bin/TDrive.exe"
[[ -f $artifact ]] || fail "build completed without producing $artifact"

target_dir=$(dirname -- "$target")
target_stage=$(mktemp "$target_dir/.TDrive.exe.update.XXXXXX") || fail "cannot stage the new executable beside $target"
marker_stage=$(mktemp "$target_dir/.TDrive.exe.commit.XXXXXX") || fail "cannot stage the update marker beside $target"

cp -- "$artifact" "$target_stage" || fail "could not copy the build beside the installed executable; check permissions and free disk space"
chmod 755 "$target_stage" || fail "could not make the staged executable runnable"
mv -f -- "$target_stage" "$target" || fail "could not replace $target; close TDrive and check that the install folder is writable"
target_stage=

printf '%s\n' "$desired_commit" > "$marker_stage"
if mv -f -- "$marker_stage" "$target_marker"; then
  marker_stage=
else
  printf 'TDrive updater: app was replaced, but the commit marker could not be saved; the next run may rebuild it.\n' >&2
fi

printf 'Updated %s to commit %s.\n' "$target" "${desired_commit:0:12}"
