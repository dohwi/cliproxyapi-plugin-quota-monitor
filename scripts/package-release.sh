#!/usr/bin/env bash
# Packages one platform's plugin library for the CLIProxyAPI plugin store:
#   dist/quota-monitor_<version>_<goos>_<goarch>.zip   (only the library, at the zip root)
# Usage: scripts/package-release.sh v0.2.0 <goos> <goarch> <library-path>
#   e.g. scripts/package-release.sh v0.2.0 linux amd64 artifacts/quota-monitor.so
# The library file name inside the zip is quota-monitor.so | quota-monitor.dylib | quota-monitor.dll by GOOS.
# Merge the per-platform zips into one release with scripts/make-checksums.sh.
set -euo pipefail

id="quota-monitor"
tag="${1:-}"; goos="${2:-}"; goarch="${3:-}"; lib="${4:-}"
[[ -n "$tag" && -n "$goos" && -n "$goarch" && -n "$lib" ]] || { echo "usage: $0 <tag> <goos> <goarch> <library>" >&2; exit 2; }
[[ "$tag" =~ ^v[0-9]+(\.[0-9]+)+$ ]] || { echo "tag '$tag' must look like v0.2.0 (store requires dotted numeric versions)" >&2; exit 1; }
case "$goos/$goarch" in
  linux/amd64|linux/arm64|darwin/amd64|darwin/arm64|windows/amd64) ;;
  *) echo "unsupported platform $goos/$goarch" >&2; exit 1 ;;
esac
case "$goos" in linux) ext=so ;; darwin) ext=dylib ;; windows) ext=dll ;; esac
version="${tag#v}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ -f "$lib" ]] || { echo "missing $lib; build it first" >&2; exit 1; }

# The official Linux image is Debian bookworm (glibc 2.36): refuse libraries needing newer symbols.
if [[ "$goos" == linux ]]; then
  max="$(objdump -T "$lib" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sed 's/GLIBC_//' | sort -V | tail -n1 || true)"
  if [[ -n "$max" && "$(printf '%s\n2.36\n' "$max" | sort -V | tail -n1)" != "2.36" ]]; then
    echo "library requires glibc $max, newer than the Debian bookworm 2.36 host image" >&2
    exit 1
  fi
fi

mkdir -p "$root/dist"
zip_name="${id}_${version}_${goos}_${goarch}.zip"
rm -f "$root/dist/$zip_name"
stage="$(mktemp -d)"; trap 'rm -rf "$stage"' EXIT
install -m 755 "$lib" "$stage/$id.$ext"
python3 - "$root/dist/$zip_name" "$stage/$id.$ext" "$id.$ext" <<'PY'
import sys, zipfile
out, src, name = sys.argv[1:4]
info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
info.external_attr = 0o755 << 16
info.compress_type = zipfile.ZIP_DEFLATED
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as z:
    with open(src, 'rb') as f:
        z.writestr(info, f.read())
PY
python3 - "$root/dist/$zip_name" "$id.$ext" <<'PY'
import sys, zipfile
names = zipfile.ZipFile(sys.argv[1]).namelist()
if names != [sys.argv[2]]:
    sys.exit("zip must contain exactly %s at its root, got %r" % (sys.argv[2], names))
PY
echo "packaged $zip_name"
