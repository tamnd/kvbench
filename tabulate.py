#!/usr/bin/env python3
# tabulate.py reads a kvbench results dir and prints markdown tables the result
# docs use, filtered by regime and durability (the built-in report collapses
# those dimensions). It is a reporting aid for the result spec, not part of the
# harness.
import json, os, sys, glob

def ns(v):
    if not v: return "-"
    v = float(v)
    if v < 1e3: return f"{v:.0f}ns"
    if v < 1e6: return f"{v/1e3:.1f}us"
    if v < 1e9: return f"{v/1e6:.2f}ms"
    return f"{v/1e9:.2f}s"

def comma(f):
    return f"{int(f):,}"

def amp(a):
    return "n/a" if a is None or a < 0 else f"{a:.2f}x"

def load(d):
    out = []
    for p in glob.glob(os.path.join(d, "*.json")):
        with open(p) as f:
            try: r = json.load(f)
            except Exception: continue
        if not r.get("engine", {}).get("name"): continue
        out.append(r)
    return out

def cell(r):
    return (r["workload"]["name"], r["workload"]["regime"], r["workload"]["durability"])

def fmt_rows(rows, cols):
    # cols: list of (header, fn)
    head = "| " + " | ".join(h for h, _ in cols) + " |"
    align = "|" + "|".join("--------:" if i else "--------" for i in range(len(cols))) + "|"
    body = []
    for r in rows:
        body.append("| " + " | ".join(fn(r) for _, fn in cols) + " |")
    return "\n".join([head, align] + body)

def main():
    d = sys.argv[1]
    regime = sys.argv[2] if len(sys.argv) > 2 else "cache-resident"
    dur = sys.argv[3] if len(sys.argv) > 3 else "NORMAL"
    rs = [r for r in load(d) if r["workload"]["regime"] == regime and r["workload"]["durability"] == dur]
    bywl = {}
    for r in rs:
        bywl.setdefault(r["workload"]["name"], []).append(r)
    cols = [
        ("engine", lambda r: r["engine"]["name"]),
        ("mode", lambda r: r["engine"]["mode"]),
        ("ops/sec", lambda r: (r.get("error") and "_"+r["error"][:40]+"_") or comma(r["throughput"]["sustained_ops"])),
        ("p50", lambda r: ns(r["latency_ns"].get("p50"))),
        ("p99", lambda r: ns(r["latency_ns"].get("p99"))),
        ("p99.9", lambda r: ns(r["latency_ns"].get("p999"))),
        ("spaceAmp", lambda r: amp(r["amplification"].get("space_amp"))),
    ]
    for wl in sorted(bywl):
        rows = bywl[wl]
        # errors sort last; otherwise by throughput desc
        rows.sort(key=lambda r: (r.get("error","")!="" , -(r["throughput"]["sustained_ops"] if not r.get("error") else 0)))
        print(f"\n## {wl}\n")
        print(fmt_rows(rows, cols))

if __name__ == "__main__":
    main()
