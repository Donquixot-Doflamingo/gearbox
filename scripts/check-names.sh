#!/usr/bin/env bash
# Fails if names that must not appear in this repository appear in tracked files (see check-names.py).
set -euo pipefail
cd "$(dirname "$0")/.."
exec python3 scripts/check-names.py "$@"
