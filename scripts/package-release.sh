#!/usr/bin/env bash
# Builds the CLIProxyAPI plugin-store release assets for linux/amd64:
#   dist/<id>_<version>_linux_amd64.zip   (contains only <id>.so at the zip root)
#   dist/checksums.txt                    (sha256sum format)
# Usage: scripts/package-release.sh v0.1.0   (defaults to $GITHUB_REF_NAME)
# Expects artifacts/<id>.so to be built already (make build).
set -euo pipefail

id="quota-monitor"
tag="${1:-${GITHUB_REF_NAME:-}}"
[[ "$tag" =~ ^v[0-9]+(\.[0-9]+)+$ ]] || { echo "tag '$tag' must look like v0.1.0 (store requires dotted numeric versions)" >&2; exit 1; }
version="${tag#v}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
lib="$root/artifacts/$id.so"
[[ -f "$lib" ]] || { echo "missing $lib; run make build first" >&2; exit 1; }

# The official image is Debian bookworm (glibc 2.36). Refuse libraries that need newer symbols.
max="$(objdump -T "$lib" | grep -oE 'GLIBC_[0-9]+\.[0-9]+' | sed 's/GLIBC_//' | sort -V | tail -n1)"
if [[ -n "$max" && "$(printf '%s\n2.36\n' "$max" | sort -V | tail -n1)" != "2.36" ]]; then
  echo "library requires glibc $max, newer than the Debian bookworm 2.36 host image" >&2
  exit 1
fi

rm -rf "$root/dist"; mkdir -p "$root/dist"
zip_name="${id}_${version}_linux_amd64.zip"
stage="$(mktemp -d)"; trap 'rm -rf "$stage"' EXIT
install -m 755 "$lib" "$stage/$id.so"
(cd "$stage" && python3 - "$root/dist/$zip_name" "$id.so" <<'PY'
import sys, zipfile
out, name = sys.argv[1:3]
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED) as z:
    info = zipfile.ZipInfo(name, (1980, 1, 1, 0, 0, 0))
    info.external_attr = 0o755 << 16
    info.compress_type = zipfile.ZIP_DEFLATED
    z.writestr(info, open(name, 'rb').read())
PY
)
(cd "$root/dist" && sha256sum "$zip_name" > checksums.txt && sha256sum -c checksums.txt)
[[ "$(unzip -Z1 "$root/dist/$zip_name")" == "$id.so" ]] || { echo "zip must contain exactly $id.so at its root" >&2; exit 1; }
echo "packaged $zip_name (glibc max ${max:-none})"
