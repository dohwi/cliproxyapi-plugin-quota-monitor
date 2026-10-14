#!/usr/bin/env bash
# Verifies that a release directory holds exactly the five store platforms for one
# version and writes checksums.txt (sha256sum format) next to the zips.
# Usage: scripts/make-checksums.sh v0.2.0 <dir-with-zips>
set -euo pipefail

id="quota-monitor"
tag="${1:-}"; dir="${2:-}"
[[ "$tag" =~ ^v[0-9]+(\.[0-9]+)+$ && -d "$dir" ]] || { echo "usage: $0 <tag> <dir>" >&2; exit 2; }
version="${tag#v}"
platforms=(darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64)

cd "$dir"
expected=()
for p in "${platforms[@]}"; do expected+=("${id}_${version}_${p}.zip"); done
for f in "${expected[@]}"; do
  [[ -f "$f" ]] || { echo "missing release asset $f" >&2; exit 1; }
done
# Nothing else may ship: a stray or misnamed zip would be offered to the wrong platform.
extra="$(ls -1 | grep -vxF -f <(printf '%s\n' "${expected[@]}" checksums.txt) || true)"
[[ -z "$extra" ]] || { echo "unexpected files in release directory:" >&2; echo "$extra" >&2; exit 1; }

sha256sum "${expected[@]}" > checksums.txt
sha256sum -c checksums.txt
echo "checksums.txt written for ${#expected[@]} platforms"
