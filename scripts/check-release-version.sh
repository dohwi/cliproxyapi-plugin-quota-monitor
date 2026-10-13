#!/usr/bin/env bash
# Fails unless the release tag matches pluginVersion in internal/plugin/plugin.go.
# Usage: scripts/check-release-version.sh v0.1.0   (defaults to $GITHUB_REF_NAME)
set -euo pipefail

tag="${1:-${GITHUB_REF_NAME:-}}"
if [[ -z "$tag" ]]; then
  echo "usage: $0 <tag>" >&2
  exit 2
fi
if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "tag '$tag' is not a semantic version like v1.2.3" >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source_version="$(sed -nE 's/^[[:space:]]*pluginVersion[[:space:]]*=[[:space:]]*"([^"]+)".*/\1/p' "$root/internal/plugin/plugin.go" | head -n1)"
if [[ -z "$source_version" ]]; then
  echo "could not read pluginVersion from internal/plugin/plugin.go" >&2
  exit 1
fi
if [[ "v$source_version" != "$tag" ]]; then
  echo "tag $tag does not match pluginVersion $source_version in internal/plugin/plugin.go" >&2
  exit 1
fi
echo "release version OK: $tag"
