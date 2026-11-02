#!/bin/bash
# macOS: double-click to start Gearbox locally (admin UI, sidecar, example services, live impact page).
# Same as `make start`. Your browser opens the UI and the live impact page.
cd "$(dirname "$0")" || exit 1
scripts/gearbox.sh start
echo; read -r -p "Gearbox keeps running in the background. Press Enter to close this window."
