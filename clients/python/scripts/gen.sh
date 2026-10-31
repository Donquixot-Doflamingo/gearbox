#!/usr/bin/env bash
# Generate Python stubs from proto/gearbox/v1/engine.proto into clients/python/gen.
# Needs: pip install grpcio-tools   (network access to PyPI)
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
proto="$here/../../proto"
out="$here/gen"
mkdir -p "$out"
python -m grpc_tools.protoc -I "$proto" \
  --python_out="$out" --grpc_python_out="$out" --pyi_out="$out" \
  "$proto/gearbox/v1/engine.proto"
touch "$out/gearbox/__init__.py" "$out/gearbox/v1/__init__.py"
echo "generated stubs in $out/gearbox/v1"
