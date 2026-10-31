# Gearbox Python client (thin wrapper)

> **Needs stub generation.** The gRPC stubs are not committed. Generate them from `proto/gearbox/v1/engine.proto` on a machine with PyPI access before installing.

Adds to the generated stub: one channel per process, a deadline on every call (default 5 ms), a fallback value when the sidecar cannot answer (`UNAVAILABLE`, `DEADLINE_EXCEEDED`, `NOT_READY`, `DECISION_NOT_FOUND`, `NODE_ERROR`), and a request ID (generated when you do not pass one). Caller errors such as `INPUT_SCHEMA` raise `GearboxError`.

## Generate stubs and install

```bash
cd clients/python
python -m venv .venv && . .venv/bin/activate
pip install grpcio-tools
./scripts/gen.sh                 # writes gen/gearbox/v1/engine_pb2*.py
pip install -e '.[dev]'
pytest                           # fallback test runs without a sidecar
```

`scripts/gen.sh` runs:

```bash
python -m grpc_tools.protoc -I ../../proto --python_out=gen --grpc_python_out=gen --pyi_out=gen \
  ../../proto/gearbox/v1/engine.proto
```

## Use

```python
from gearbox_client import Client

client = Client()  # GEARBOX_TARGET or unix:///var/run/gearbox/engine.sock
d = client.decide("lead_routing", lead, unit_id=lead["lead"]["id"],
                  timeout=0.005, fallback={"route": "manual_review"})
log.info("route=%s version=%s hash=%s fallback=%s",
         d.output["route"], d.version, d.dependency_hash, d.from_client_fallback)
```

`check_gate(gate, unit_id, attributes, fallback=False)` and `get_variant(experiment, unit_id)` are thin passthroughs.

Rules: reuse one `Client`; never retry against the local sidecar; log `version` and `dependency_hash` with your business event.
