"""Integration test against a real gearbox-sidecar serving examples/definitions.

Skipped unless GEARBOX_TEST_SIDECAR points at a running sidecar socket, for example:

    gearbox-sidecar --definitions-dir examples/definitions --allow-unsigned \
        GEARBOX_SOCKET=/tmp/gbx.sock  &
    GEARBOX_TEST_SIDECAR=unix:///tmp/gbx.sock pytest
"""

import os

import pytest

from gearbox_client import Client, GearboxError

TARGET = os.environ.get("GEARBOX_TEST_SIDECAR")
pytestmark = pytest.mark.skipif(not TARGET, reason="GEARBOX_TEST_SIDECAR not set")

LEAD = {
    "lead": {"id": "L-1", "city": "Gurgaon", "source": "app"},
    "car": {"make": "Maruti", "model": "Swift", "year": 2019, "km": 48200, "price": 525000},
    "inspections": [{"part": "engine", "score": 8}],
}


def test_decide_returns_output_and_version():
    with Client(TARGET, default_timeout=1.0) as c:
        d = c.decide("lead_routing", LEAD, unit_id="L-1", trace=True)
        assert not d.from_client_fallback
        assert d.output["route"] in {"fast_track", "standard", "reject", "manual_review"}
        assert d.version.startswith("lead_routing@")
        assert d.dependency_hash.startswith("sha256:")
        assert [step[0] for step in d.path][0] == "normalize"


def test_blocked_city_is_rejected():
    lead = {**LEAD, "lead": {"id": "L-2", "city": "Leh", "source": "web"}}
    with Client(TARGET, default_timeout=1.0) as c:
        d = c.decide("lead_routing", lead, unit_id="L-2")
        assert d.output == {"route": "reject", "reason": "city_not_served"}


def test_input_schema_error_is_raised_not_hidden():
    with Client(TARGET, default_timeout=1.0) as c:
        with pytest.raises(GearboxError) as e:
            c.decide("lead_routing", {"lead": {"id": "x"}}, unit_id="x", fallback={"route": "manual_review"})
        assert e.value.code.name == "INVALID_ARGUMENT"


def test_unknown_decision_falls_back():
    with Client(TARGET, default_timeout=1.0) as c:
        d = c.decide("no_such_decision", LEAD, fallback={"route": "manual_review"})
        assert d.from_client_fallback and d.output == {"route": "manual_review"}
