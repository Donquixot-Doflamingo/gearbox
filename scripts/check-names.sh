#!/usr/bin/env bash
# Fails if company names or internal identifiers appear in tracked files.
set -euo pipefail
cd "$(dirname "$0")/.."
pattern='cars24|cspl|\bc24\b|c24-|bifrost|pitlane|cosmos-feature|statsig|swbd|switchboard'
if git grep -nIiE "$pattern" -- . ':!scripts/check-names.sh' ':!docs/archive/**'; then
  echo "check-names: forbidden names found" >&2; exit 1
fi
echo "check-names: ok"
