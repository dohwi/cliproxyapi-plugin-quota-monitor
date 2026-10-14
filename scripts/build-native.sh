#!/usr/bin/env bash
# Builds the c-shared plugin library for the host platform and runs the native ABI smoke
# test against it. Needs only bash, go, and a C compiler (gcc/clang), so it behaves the
# same on Linux, macOS and Windows (Git Bash + mingw-w64 gcc), where make may be absent.
# Usage: scripts/build-native.sh [--no-smoke]
set -euo pipefail

id="quota-monitor"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

goos="$(go env GOOS)"
case "$goos" in
  linux)   ext=so;    smoke_exe=native-smoke;     libs="-ldl" ;;
  darwin)  ext=dylib; smoke_exe=native-smoke;     libs="" ;;
  windows) ext=dll;   smoke_exe=native-smoke.exe; libs="" ;;
  *) echo "unsupported GOOS $goos" >&2; exit 1 ;;
esac
lib="artifacts/$id.$ext"

export CGO_ENABLED=1
mkdir -p artifacts
go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o "$lib" .
if command -v sha256sum >/dev/null 2>&1; then sha256sum "$lib"; else shasum -a 256 "$lib"; fi

[[ "${1:-}" == "--no-smoke" ]] && exit 0
${CC:-gcc} -O2 -Wall -Wextra -Werror -o "artifacts/$smoke_exe" verification/native_smoke.c $libs

"artifacts/$smoke_exe" "$lib"
rm -f "artifacts/$smoke_exe"
