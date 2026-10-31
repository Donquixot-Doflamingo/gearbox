"""Thin Python wrapper for the Gearbox sidecar.

Stubs are generated from proto/gearbox/v1/engine.proto (see README); this
package adds a deadline, a fallback value and request-ID handling.
"""

from .client import Client, Decision, GearboxError

__all__ = ["Client", "Decision", "GearboxError"]
