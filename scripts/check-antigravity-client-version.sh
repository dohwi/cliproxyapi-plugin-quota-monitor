#!/usr/bin/env bash
# Compare the pinned Antigravity client version (User-Agent) with the official
# stable version. Google omits quota groups (e.g. "Gemini Models") for clients
# that are too old, so drift must be noticed. See docs/antigravity-quota.md.
#
# Exit codes: 0 = in sync, 1 = drift (pinned is older/different), 2 = lookup failed.
set -euo pipefail

cd "$(dirname "$0")/.."
UPDATER_URL="${ANTIGRAVITY_UPDATER_URL:-https://antigravity-cli-auto-updater-974169037036.us-central1.run.app}"

pinned="$(sed -nE 's#.*antigravityUserAgent = "antigravity/cli/([0-9]+(\.[0-9]+)+) .*#\1#p' internal/quota/quota.go | head -n 1)"
if [ -z "$pinned" ]; then
  echo "could not find antigravityUserAgent in internal/quota/quota.go" >&2
  exit 2
fi

body="$(curl -fsS -m 20 "$UPDATER_URL" 2>/dev/null || true)"
latest="$(printf '%s' "$body" | sed -nE 's#.*Stable Version: ([0-9]+(\.[0-9]+)+).*#\1#p' | head -n 1)"
if [ -z "$latest" ]; then
  echo "could not read the stable version from the Antigravity updater (format changed or offline)" >&2
  exit 2
fi

if [ "$pinned" = "$latest" ]; then
  echo "antigravity client version in sync: $pinned"
  exit 0
fi

echo "antigravity client version drift: pinned=$pinned stable=$latest"
echo "Follow docs/antigravity-quota.md: verify groups with the new User-Agent, then bump antigravityUserAgent."
exit 1
