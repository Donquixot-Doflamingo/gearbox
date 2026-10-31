"""Runs without a sidecar: the call must fall back within the deadline."""

import time

from gearbox_client import Client


def test_fallback_when_sidecar_missing(tmp_path):
    with Client(f"unix://{tmp_path}/missing.sock", default_timeout=0.005) as c:
        start = time.monotonic()
        d = c.decide("lead_routing", {"lead": {"id": "L-1"}}, unit_id="L-1",
                     fallback={"route": "manual_review"})
        assert d.from_client_fallback
        assert d.output == {"route": "manual_review"}
        assert time.monotonic() - start < 0.5
