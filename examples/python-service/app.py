"""Example Python backend that asks the local Gearbox sidecar for decisions.

    POST /route   body: a lead (lead_input schema)  → {"route", "reason", "version", ...}
    GET  /healthz

Env: GEARBOX_TARGET (default unix:///var/run/gearbox/engine.sock), EXAMPLE_ADDR (default 127.0.0.1:8082).
Uses only the standard library plus the Gearbox Python client (clients/python).
"""

from __future__ import annotations

import json
import logging
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from gearbox_client import Client, GearboxError

log = logging.getLogger("python-service")
CLIENT = Client(os.environ.get("GEARBOX_TARGET"), default_timeout=0.005)  # one client per process
FALLBACK = {"route": "manual_review", "reason": "client_fallback"}


class Handler(BaseHTTPRequestHandler):
    def _send(self, code: int, body: dict) -> None:
        raw = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/healthz":
            return self._send(200, {"ok": True})
        self._send(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/route":
            return self._send(404, {"error": "not found"})
        try:
            lead = json.loads(self.rfile.read(int(self.headers.get("content-length", 0))) or b"{}")
        except json.JSONDecodeError:
            return self._send(400, {"error": "body must be JSON"})
        unit = ((lead.get("lead") or {}).get("id")) or ""
        try:
            d = CLIENT.decide("lead_routing", lead, unit_id=unit, fallback=FALLBACK)
        except GearboxError as e:  # caller error, e.g. input does not match lead_input
            return self._send(422, {"error": e.code.name, "message": e.message})
        out = d.output if isinstance(d.output, dict) else {"output": d.output}
        log.info("route=%s version=%s generation=%s fallback=%s", out.get("route"), d.version, d.generation, d.from_client_fallback)
        self._send(200, {**out, "version": d.version, "dependencyHash": d.dependency_hash, "generation": d.generation,
                         "fallbackUsed": d.fallback_used, "fromClientFallback": d.from_client_fallback, "service": "python"})

    def log_message(self, *args) -> None:  # quiet access log
        pass


def main() -> None:
    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")
    host, _, port = os.environ.get("EXAMPLE_ADDR", "127.0.0.1:8082").rpartition(":")
    srv = ThreadingHTTPServer((host or "0.0.0.0", int(port)), Handler)
    log.info("listening on %s:%s, sidecar %s", host, port, os.environ.get("GEARBOX_TARGET", "unix:///var/run/gearbox/engine.sock"))
    srv.serve_forever()


if __name__ == "__main__":
    main()
