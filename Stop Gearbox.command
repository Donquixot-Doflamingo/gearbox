#!/bin/bash
# macOS: double-click to stop everything "Start Gearbox.command" started. Same as `make stop`.
cd "$(dirname "$0")" || exit 1
scripts/gearbox.sh stop
sleep 2
