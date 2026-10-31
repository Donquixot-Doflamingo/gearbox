"""Latency and throughput of Python client → sidecar calls (real measurements, nothing simulated).

    python bench.py [--target unix:///tmp/gbx.sock] [--calls 5000] [--threads 1,4,8]
"""

import argparse
import json
import os
import statistics
import threading
import time

from gearbox_client import Client

LEAD = {"lead": {"id": "L-77", "city": "Indore", "source": "web"},
        "car": {"make": "Hyundai", "model": "i20", "year": 2021, "km": 12000, "price": 690000},
        "inspections": [{"part": "engine", "score": 6}]}


def pct(xs, p):
    return xs[min(len(xs) - 1, int(p * (len(xs) - 1)))]


def run(client, calls, threads):
    """calls split over `threads` threads that share one client (one client per process, as in a service)."""
    lat, lock = [], threading.Lock()
    per = calls // threads

    def worker(i):
        mine = []
        for n in range(per):
            t0 = time.perf_counter()
            client.decide("lead_routing", LEAD, unit_id=f"u{i}-{n}", timeout=2.0)
            mine.append((time.perf_counter() - t0) * 1e6)
        with lock:
            lat.extend(mine)

    t0 = time.perf_counter()
    ts = [threading.Thread(target=worker, args=(i,)) for i in range(threads)]
    [t.start() for t in ts]
    [t.join() for t in ts]
    wall = time.perf_counter() - t0
    lat.sort()
    return {"client": "python", "threads": threads, "calls": len(lat), "perMinute": round(len(lat) / wall * 60),
            "p50Micros": round(pct(lat, .5)), "p95Micros": round(pct(lat, .95)), "p99Micros": round(pct(lat, .99)),
            "meanMicros": round(statistics.mean(lat))}


if __name__ == "__main__":
    ap = argparse.ArgumentParser()
    ap.add_argument("--target", default=os.environ.get("GEARBOX_TARGET", "unix:///tmp/gbx.sock"))
    ap.add_argument("--calls", type=int, default=5000)
    ap.add_argument("--threads", default="1,4,8")
    a = ap.parse_args()
    client = Client(a.target, default_timeout=2.0)
    for _ in range(500):  # warm up the channel
        client.decide("lead_routing", LEAD, unit_id="warm")
    for t in [int(x) for x in a.threads.split(",")]:
        print(json.dumps(run(client, a.calls, t)))
