"""ppanalyze.py [out/power]: energy per doc from the ppseq.sh traces.

System power is Power Profiler's SystemPowerLevel (battery %/hr, ~1 Hz, only
on battery). The doc loop is the app's last docWallSec before endEpoch
(queries skipped, no batched pass), trimmed by TRIM s at each end. Idle is
the idle trace recorded just before the run, its first 15 s dropped.
Per-process CPU/GPU impact is Power Profiler's unitless power impact;
the Neural Engine has no power column, only busy intervals (ane-hw-intervals).
Idle is the median of the idle traces. "steady" is the last STEADY s of the
doc loop, after the phone has settled to its sustained power level; its
docs/s is the time-weighted rate of the passes in that span.
J use WH (15.5 Wh SIM-tray / 16.6 Wh eSIM iPhone 17 Pro; set WH=)."""
import json, os, subprocess, sys
import xml.etree.ElementTree as ET
from datetime import datetime

P = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "out/power")
WH = float(os.environ.get("WH", "16.0"))
TRIM = float(os.environ.get("TRIM", "5"))


def export(tr, xpath):
    x = subprocess.run(["xcrun", "xctrace", "export", "--input", tr, "--xpath", xpath],
                       capture_output=True, text=True).stdout
    return ET.fromstring(x)


def table(tr, schema):
    root = export(tr, f'/trace-toc/run[@number="1"]/data/table[@schema="{schema}"]')
    node = root.find("node")
    if node is None:
        return []
    cols = [c.findtext("mnemonic") for c in node.find("schema").findall("col")]
    ids, out = {}, []
    for row in node.findall("row"):
        vals = []
        for el in row:
            if el.tag == "sentinel":
                vals.append(None)
                continue
            if "ref" in el.attrib:
                el = ids[el.attrib["ref"]]
            else:
                for sub in el.iter():
                    if "id" in sub.attrib:
                        ids[sub.attrib["id"]] = sub
            vals.append(el)
        out.append(dict(zip(cols, vals)))
    return out


def num(el):
    return float(el.text) if el is not None and el.text not in (None, "") else 0.0


def start_epoch(tr):
    toc = subprocess.run(["xcrun", "xctrace", "export", "--input", tr, "--toc"], capture_output=True, text=True).stdout
    s = ET.fromstring(toc).find(".//summary/start-date").text
    return datetime.fromisoformat(s).timestamp()


def power(tr):
    rows = []
    for r in table(tr, "SystemPowerLevel"):
        t, d = num(r["start"]) / 1e9, num(r["duration"]) / 1e9
        rows.append((t, t + d, num(r["power-usage"]), r["display-brightness"].text if r.get("display-brightness") is not None else None))
    return rows


def wmean(rows, a, b, f=lambda r: r[2]):
    tot = w = 0.0
    for r in rows:
        lo, hi = max(a, r[0]), min(b, r[1])
        if hi > lo:
            tot += f(r) * (hi - lo)
            w += hi - lo
    return tot / w if w else float("nan"), w


def proc_impact(tr, a, b):
    rows = []
    for r in table(tr, "ProcessSubsystemPowerImpact"):
        if r["row-kind"].text != "0":
            continue
        t, d = num(r["start"]) / 1e9, num(r["duration"]) / 1e9
        rows.append((t, t + d, num(r["cpu-impact"]), num(r["gpu-impact"]), num(r["cpu-instructions"])))
    cpu, _ = wmean(rows, a, b, lambda r: r[2])
    gpu, _ = wmean(rows, a, b, lambda r: r[3])
    ins = sum(r[4] * max(0, min(b, r[1]) - max(a, r[0])) / (r[1] - r[0]) for r in rows if r[1] > r[0])
    return cpu, gpu, ins / (b - a)


def ane_busy(tr, a, b):
    busy = n = 0
    for r in table(tr, "ane-hw-intervals"):
        lab = r["event-label"].attrib.get("fmt", "")
        if "Load" in lab:
            continue
        t, d = num(r["start"]) / 1e9, num(r["duration"]) / 1e9
        lo, hi = max(a, t), min(b, t + d)
        if hi > lo:
            busy += hi - lo
            n += 1
    return busy / (b - a), n


def idle_level(name):
    tr = os.path.join(P, name + ".trace")
    rows = power(tr)
    end = rows[-1][1]
    m, w = wmean(rows, rows[0][0] + 15, end - 3)
    return m, w, rows


RUNS = ["pp-nemosine4", "pp-eg2-q8-metal", "pp-qwen3-q8-metal"]
STEADY = float(os.environ.get("STEADY", "100"))
idles = []
for n in sorted(f[:-5] for f in os.listdir(P) if f.startswith("pp-idle-") and f.endswith(".json")):
    m, w, rows = idle_level(n)
    idles.append(m)
    print(f"{n}: {m:.2f} %/hr over {w:.0f} s, brightness {sorted({r[3] for r in rows})}")
idles.sort()
IDLE = float(os.environ.get("IDLE", idles[len(idles) // 2] if len(idles) % 2 else sum(idles[len(idles) // 2 - 1:len(idles) // 2 + 1]) / 2))
print(f"idle {IDLE:.2f} %/hr = {IDLE / 100 * WH:.2f} W at {WH} Wh")


def jdoc(pct, dps):  # J per doc above idle
    return (pct - IDLE) / 100 * WH / dps


res = []
for run in RUNS + ([os.environ["RUN"]] if os.environ.get("RUN") else []):
    tr, js = os.path.join(P, run + ".trace"), os.path.join(P, run + ".json")
    if not (os.path.exists(tr) and os.path.exists(js)):
        continue
    d = json.load(open(js))
    t0 = start_epoch(tr)
    end = d["endEpoch"] - t0
    s0 = end - d["docWallSec"]
    a, b = s0 + TRIM, end - TRIM
    rows = power(tr)
    m, w = wmean(rows, a, b)
    dps = d["docs"] * d["repeats"] / d["docWallSec"]
    passes, s = [], s0
    pd = d.get("passDocsPerSec") or [dps]
    sa, sw, sx = end - STEADY, 0.0, 0.0
    for i, x in enumerate(pd):
        dur = d["docs"] / x
        pm, _ = wmean(rows, s + (TRIM if i == 0 else 0), s + dur - (TRIM if i == len(pd) - 1 else 0))
        passes.append({"dps": round(x, 1), "pctPerHr": round(pm, 1), "JPerDoc": round(jdoc(pm, x), 4)})
        ov = max(0.0, min(end - TRIM, s + dur) - max(sa, s))
        sw, sx = sw + ov, sx + ov * x
        s += dur
    sdps = sx / sw
    spct, _ = wmean(rows, sa, end - TRIM)
    cpu, gpu, ips = proc_impact(tr, a, b)
    ane, nane = ane_busy(tr, a, b)
    r = {"run": run, "docsPerSec": round(dps, 1), "windowSec": round(w, 1), "sysPctPerHr": round(m, 2),
         "incW": round((m - IDLE) / 100 * WH, 2), "JPerDoc": round(jdoc(m, dps), 4),
         "pctBatteryPer1000Docs": round((m - IDLE) / 3600 * 1000 / dps, 4),
         "steady": {"dps": round(sdps, 1), "pctPerHr": round(spct, 1), "incW": round((spct - IDLE) / 100 * WH, 2),
                    "JPerDoc": round(jdoc(spct, sdps), 4), "pctBatteryPer1000Docs": round((spct - IDLE) / 3600 * 1000 / sdps, 4)},
         "passes": passes, "procCpuImpact": round(cpu, 2), "procGpuImpact": round(gpu, 2),
         "cpuInstrPerSec": float(f"{ips:.3g}"), "aneBusy": round(ane, 3), "aneIntervals": nane,
         "thermal": d["sampler"].get("thermalChanges"), "peakMB": round(d.get("peakFootprintMB") or d["sampler"].get("maxFootprintMB")),
         "batteryAtStart": d.get("device", {}).get("battery")}
    res.append(r)
    print(json.dumps(r))
json.dump({"idlePctPerHr": IDLE, "wh": WH, "runs": res}, open(os.path.join(P, "ppanalyze.json"), "w"), indent=1)
