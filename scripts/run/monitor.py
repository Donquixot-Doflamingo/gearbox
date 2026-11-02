"""Live impact page for a local Gearbox: what each example service answers right now.

Started by `scripts/gearbox.sh start`; serves http://127.0.0.1:<port>/ (stdlib only).
Every second the page asks the Go, Python and Java example services to route the same
sample leads, and shows each answer, the decision version, the bundle generation the
sidecar serves, and the call time. Change something in the Gearbox UI and watch the
answers change here.
"""

import json
import os
import sys
import time
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("MONITOR_PORT", "8099"))
UI_URL = os.environ.get("GEARBOX_UI_URL", "http://127.0.0.1:8080")
SIDECAR_METRICS = os.environ.get("SIDECAR_METRICS", "http://127.0.0.1:9464")
SERVICES = [(name, os.environ.get(f"{name.upper()}_URL", "")) for name in ("go", "python", "java")]

# Local calls only: never send them through an HTTP proxy from the environment.
OPENER = urllib.request.build_opener(urllib.request.ProxyHandler({}))

LEADS = [
    ("Indore · web · 2021 i20", {"lead": {"id": "L-77", "city": "Indore", "source": "web"},
                                 "car": {"make": "Hyundai", "model": "i20", "year": 2021, "km": 12000, "price": 690000},
                                 "inspections": [{"part": "engine", "score": 6}]}),
    ("Pune · app · 2020", {"lead": {"id": "L-4", "city": "Pune", "source": "app"},
                           "car": {"make": "Maruti", "model": "Baleno", "year": 2020, "km": 30000, "price": 650000},
                           "inspections": [{"part": "engine", "score": 9}, {"part": "body", "score": 8}]}),
    ("Pune · app · 2010 (old car)", {"lead": {"id": "L-3", "city": "Pune", "source": "app"},
                                     "car": {"make": "Honda", "model": "City", "year": 2010, "km": 160000, "price": 210000}}),
    ("Leh · web (blocked city)", {"lead": {"id": "L-2", "city": "Leh", "source": "web"},
                                  "car": {"make": "Tata", "model": "Nexon", "year": 2021, "km": 12000, "price": 690000}}),
]


def route(base, lead):
    if not base:
        return {"state": "not started"}
    t0 = time.perf_counter()
    try:
        req = urllib.request.Request(base + "/route", data=json.dumps(lead).encode(), method="POST",
                                     headers={"content-type": "application/json"})
        with OPENER.open(req, timeout=2) as r:
            d = json.load(r)
        d["ms"] = round((time.perf_counter() - t0) * 1000, 1)
        d["state"] = "ok"
        return d
    except Exception as e:  # service down or not built
        return {"state": "down", "error": e.__class__.__name__}


def generation():
    try:
        with OPENER.open(SIDECAR_METRICS + "/metrics", timeout=1) as r:
            for line in r.read().decode().splitlines():
                if line.startswith("gearbox_bundle_generation "):
                    return int(float(line.split()[1]))
    except Exception:
        return None


POOL = ThreadPoolExecutor(16)


def state():
    jobs = {(s, i): POOL.submit(route, url, lead) for s, url in SERVICES for i, (_, lead) in enumerate(LEADS)}
    rows = []
    for i, (label, lead) in enumerate(LEADS):
        rows.append({"lead": label, "answers": {s: jobs[(s, i)].result() for s, _ in SERVICES}})
    return {"time": time.strftime("%H:%M:%S"), "generation": generation(), "ui": UI_URL,
            "services": [s for s, _ in SERVICES], "rows": rows}


PAGE = """<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>Gearbox · live impact</title>
<style>
:root{--bg:#f7f7f5;--fg:#1c1c1a;--mut:#6b6b66;--card:#fff;--line:#e4e4df;--ok:#1d7a46;--okbg:#e5f4ea;--bad:#b42318;--badbg:#fdecea;--warn:#8a5a00;--warnbg:#fff4dc;--acc:#2546d9}
@media (prefers-color-scheme:dark){:root{--bg:#141413;--fg:#ecece8;--mut:#9a9a94;--card:#1d1d1b;--line:#2e2e2b;--ok:#6fd39a;--okbg:#17301f;--bad:#ff8a80;--badbg:#3a1a17;--warn:#f0c060;--warnbg:#3a2e12;--acc:#8fa2ff}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.45 ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:1100px;margin:0 auto;padding:24px 16px}h1{font-size:20px;margin:0 0 4px}p{margin:0;color:var(--mut)}
header{display:flex;flex-wrap:wrap;gap:12px;justify-content:space-between;align-items:end;margin-bottom:16px}
a{color:var(--acc)}.pill{display:inline-block;padding:2px 8px;border-radius:99px;background:var(--card);border:1px solid var(--line);font-variant-numeric:tabular-nums}
.wrap{overflow-x:auto;background:var(--card);border:1px solid var(--line);border-radius:10px}
table{border-collapse:collapse;width:100%;min-width:640px}th,td{text-align:left;padding:10px 12px;border-bottom:1px solid var(--line);vertical-align:top}
th{font-size:12px;color:var(--mut);font-weight:600;text-transform:uppercase;letter-spacing:.04em}tr:last-child td{border-bottom:0}
.route{font-weight:600;padding:1px 6px;border-radius:5px}.fast_track,.standard{color:var(--ok);background:var(--okbg)}.reject{color:var(--bad);background:var(--badbg)}.manual_review{color:var(--warn);background:var(--warnbg)}
.meta{display:block;color:var(--mut);font-size:12px;font-variant-numeric:tabular-nums}.flash{animation:f 1.2s}@keyframes f{from{outline:2px solid var(--acc)}to{outline:2px solid transparent}}
.down{color:var(--mut)}ol{color:var(--mut);padding-left:18px;margin:16px 0 0}
</style></head><body><main>
<header><div><h1>Gearbox · live impact</h1><p>What the Go, Python and Java example services answer right now, refreshed every second.</p></div>
<div><span class="pill" id="gen">generation –</span> <span class="pill" id="t">–</span> <a class="pill" id="ui" href="#" target="_blank">Open Gearbox UI ↗</a></div></header>
<div class="wrap"><table><thead><tr id="head"><th>Sample lead</th></tr></thead><tbody id="body"></tbody></table></div>
<ol><li>Open the Gearbox UI → <b>Segments → blocked_cities → Members</b>, add <b>Indore</b>, save.</li>
<li><b>Versions → Cut version</b> → <b>Request release</b> (dev) → <b>Publish to dev</b>.</li>
<li>Within about 2 seconds the Indore row turns <b>reject / city_not_served</b> in every service, and the generation goes up.</li></ol>
</main><script>
const prev = {};
function cell(a){
  if(!a || a.state!=="ok") return `<span class="down">${a ? (a.state==="not started" ? "not started" : "down") : "–"}</span>`;
  const fb = a.fromClientFallback ? " · client fallback" : "";
  return `<span class="route ${a.route}">${a.route}</span> ${a.reason ?? ""}<span class="meta">${a.version || "–"} · gen ${a.generation ?? "–"} · ${a.ms} ms round trip${fb}</span>`;
}
async function tick(){
  try{
    const s = await (await fetch("api/state")).json();
    document.getElementById("gen").textContent = "generation " + (s.generation ?? "– (sidecar down)");
    document.getElementById("t").textContent = s.time;
    document.getElementById("ui").href = s.ui;
    document.getElementById("head").innerHTML = "<th>Sample lead</th>" + s.services.map(x=>`<th>${x} service</th>`).join("");
    document.getElementById("body").innerHTML = s.rows.map((r,i)=>"<tr><td>"+r.lead+"</td>"+s.services.map(x=>{
      const a=r.answers[x], k=i+x, v=a&&a.state==="ok"?a.route+a.reason+a.generation:"";
      const fl = prev[k]!==undefined && prev[k]!==v; prev[k]=v;
      return `<td class="${fl?"flash":""}">${cell(a)}</td>`}).join("")+"</tr>").join("");
  }catch(e){ document.getElementById("t").textContent = "monitor stopped"; }
}
tick(); setInterval(tick, 1000);
</script></body></html>"""


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/api/state"):
            body, ctype = json.dumps(state()).encode(), "application/json"
        elif self.path in ("/", "/index.html"):
            body, ctype = PAGE.encode(), "text/html; charset=utf-8"
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("content-type", ctype)
        self.send_header("cache-control", "no-store")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


if __name__ == "__main__":
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"monitor on http://127.0.0.1:{PORT}", file=sys.stderr)
    srv.serve_forever()
