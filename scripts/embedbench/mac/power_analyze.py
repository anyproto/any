"""power_analyze.py <power-dir>: average whole-system power per run from
AppleSmartBattery accumulators (acc.log) over the publication windows
that fall inside each run (runs.log), and energy per 1000 docs."""
import json
import os
import sys

P = sys.argv[1]
acc = []
for l in open(os.path.join(P, "acc.log")):
    f = l.split()
    if len(f) == 7 and all(x.isdigit() for x in f):
        acc.append(tuple(int(x) for x in f))
# Publication points: first observation of each new SystemLoad count.
pts, last = [], None
for r in acc:
    if r[2] != last:
        pts.append(r)
        last = r[2]

runs = []
for l in open(os.path.join(P, "runs.log")):
    f = l.split()
    if len(f) == 3:
        runs.append((f[0], int(f[1]), int(f[2])))


def window(st, en, warm):
    inside = [p for p in pts if p[0] >= st + warm and p[0] <= en]
    if len(inside) < 2:
        return None
    a, b = inside[0], inside[-1]
    dl, dc = b[1] - a[1], b[2] - a[2]
    din, dcin = b[3] - a[3], b[4] - a[4]
    dbat, dcbat = b[5] - a[5], b[6] - a[6]
    return {"windowSec": b[0] - a[0], "samples": dc, "sysLoadW": dl / dc / 1000,
            "powerInW": (din / dcin / 1000) if dcin else None,
            "battSamples": dcbat, "battW": (dbat / dcbat / 1000) if dcbat else 0.0}


idle = [window(st, en, 5) for n, st, en in runs if n.startswith("idle")]
idle = [w for w in idle if w]
idleW = sum(w["sysLoadW"] * w["samples"] for w in idle) / sum(w["samples"] for w in idle)
print("idle windows:", [(round(w["sysLoadW"], 2), w["samples"]) for w in idle], "-> idle %.2f W" % idleW)
print("%-16s %8s %7s %8s %8s %9s %12s %12s" % ("run", "window s", "docs/s", "sys W", "net W", "batt W", "J/1k gross", "J/1k net"))
for n, st, en in runs:
    if n.startswith("idle"):
        continue
    w = window(st, en, 15)
    jp = os.path.join(P, n + ".json")
    if not w or not os.path.exists(jp):
        print(n, "no window", w)
        continue
    dps = json.load(open(jp))["docsPerSec"]
    print("%-16s %8d %7.2f %8.2f %8.2f %9.2f %12.0f %12.0f" % (
        n, w["windowSec"], dps, w["sysLoadW"], w["sysLoadW"] - idleW, w["battW"],
        w["sysLoadW"] / dps * 1000, (w["sysLoadW"] - idleW) / dps * 1000))
