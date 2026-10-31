"""Drives the real admin UI with a headless browser and shows the change reaching every service.

Run while scripts/demo/live-demo.sh is running (UI built into ui/build):
    pip install playwright && python -m playwright install chromium
    python scripts/demo/ui_demo.py --out .data/demo/ui
Steps (all through the UI): add "Indore" to the blocked_cities segment → cut a version →
open a change request for dev → approve → publish. Then it polls the Go, Python and Java
services and records how long each took to change its answer. Screenshots + a video are saved.
"""

import argparse
import json
import os
import time
import urllib.request

from playwright.sync_api import sync_playwright

UI = "http://127.0.0.1:" + os.environ.get("GEARBOX_DEMO_PORT", "8080")
A = UI + "/apps/lead_service"
LEAD = json.dumps({"lead": {"id": "L-77", "city": "Indore", "source": "web"},
                   "car": {"make": "Hyundai", "model": "i20", "year": 2021, "km": 12000, "price": 690000},
                   "inspections": [{"part": "engine", "score": 6}]}).encode()
SERVICES = {"go": 8081, "python": 8082, "java": 8083}


def ask(port):
    try:
        req = urllib.request.Request(f"http://127.0.0.1:{port}/route", data=LEAD, method="POST")
        d = json.load(urllib.request.urlopen(req, timeout=1))
        return f'{d.get("route")}/{d.get("reason")} gen={d.get("generation")}'
    except Exception as e:  # service not running
        return f"unavailable ({e.__class__.__name__})"


def current_members():
    """The segment's current members, so the demo only adds a city and never drops others."""
    url = UI + "/api/v1/apps/lead_service/segments/blocked_cities/members?limit=1000"
    return json.load(urllib.request.urlopen(url, timeout=5))["items"]


def snapshot():
    return {name: ask(port) for name, port in SERVICES.items()}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=".data/demo/ui")
    ap.add_argument("--city", default="Indore")
    a = ap.parse_args()
    log = []

    def note(msg):
        line = f"{time.strftime('%H:%M:%S')} {msg}"
        print(line, flush=True)
        log.append(line)

    with sync_playwright() as p:
        b = p.chromium.launch()
        ctx = b.new_context(viewport={"width": 1280, "height": 800}, record_video_dir=a.out, record_video_size={"width": 1280, "height": 800})
        page = ctx.new_page()
        shot = lambda n: page.screenshot(path=f"{a.out}/{n}.png")

        note(f"services before: {snapshot()}")
        page.goto(A + "?env=dev", wait_until="networkidle"); page.wait_for_timeout(600); shot("01-overview")

        page.goto(A + "/segments/blocked_cities?env=dev&tab=members", wait_until="networkidle"); page.wait_for_timeout(500)
        members = [m for m in current_members() if m != a.city] + [a.city]
        page.fill("#sg-paste", "\n".join(members))
        page.get_by_role("button", name="Replace members").click(); page.wait_for_timeout(1200); shot("02-members-replaced")
        note(f"UI: replaced blocked_cities members (added {a.city})")

        page.goto(A + "/segments/blocked_cities?env=dev&tab=versions", wait_until="networkidle"); page.wait_for_timeout(500)
        page.fill("#cut-msg", f"Block {a.city}")
        page.get_by_role("button", name="Cut v").click(); page.wait_for_timeout(1500); shot("03-version-cut")
        note("UI: cut a new version")

        page.get_by_role("link", name="Request release").first.click(); page.wait_for_load_state("networkidle"); page.wait_for_timeout(500)
        page.select_option("#cr-env", "dev")
        page.get_by_role("button", name="Open change request").click()
        page.wait_for_url("**/changes/**", timeout=15000); page.wait_for_load_state("networkidle"); page.wait_for_timeout(800); shot("04-change-request")
        note(f"UI: opened change request {page.url.rsplit('/', 1)[-1].split('?')[0]}")

        approve = page.get_by_role("button", name="Approve", exact=True)
        if approve.count() and approve.first.is_enabled():
            approve.first.click(); page.wait_for_timeout(300)
            page.get_by_role("dialog").get_by_role("button", name="Approve").click(); page.wait_for_timeout(1200)
            note("UI: approved")
        page.get_by_role("button", name="Publish to dev").first.click(); page.wait_for_timeout(300)
        t_publish = time.time()
        page.get_by_role("dialog").get_by_role("button", name="Publish to dev").click()

        # Poll the services right away (every 50 ms) so the time measured is click → new answer,
        # not including the screenshot below.
        flipped = {}
        while time.time() - t_publish < 20 and len(flipped) < len(SERVICES):
            for name, port in SERVICES.items():
                if name not in flipped and "city_not_served" in ask(port):
                    flipped[name] = round(time.time() - t_publish, 2)
            time.sleep(0.05)
        note("UI: published to dev")
        page.wait_for_timeout(500); shot("05-published")
        note(f"seconds from Publish click to new answer: {flipped}")
        note(f"services after: {snapshot()}")

        page.goto(A + "/fleet?env=dev", wait_until="networkidle"); page.wait_for_timeout(800); shot("06-fleet")
        page.goto(A + "/releases?env=dev", wait_until="networkidle"); page.wait_for_timeout(800); shot("07-releases")
        page.goto(A + "/segments/blocked_cities?env=dev&tab=members", wait_until="networkidle"); page.wait_for_timeout(800); shot("08-segment-members")
        ctx.close(); b.close()
    open(f"{a.out}/ui-demo.log", "w").write("\n".join(log) + "\n")


if __name__ == "__main__":
    main()
