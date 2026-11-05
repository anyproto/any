"""lenan.py <lenexp.json>: EG2 vs Qwen3 by chunk length and strategy, from
lenexp's rank dumps. Cells R@1 / MRR@10; Δ MRR (EG2 − Qwen3) with a paired
bootstrap 95% CI over queries."""
import json, random, sys

base = sys.argv[1].rsplit(".json", 1)[0]
R = {k: json.load(open(f"{base}.{k}.ranks.json")) for k in ("eg2", "qwen3")}
data = json.load(open(sys.argv[1]))
qt = R["qwen3"]["tokens"]
et = R["eg2"]["tokens"]
over_e = sum(t > 512 for t in et)
over_q = sum(t > 512 for t in qt)
print(f"chunks over 512 tokens: EG2 {over_e}, Qwen3 {over_q} (same {len(et)} chunks)")

rr = lambda r: 1 / r if r <= 10 else 0.0


def boot(d, n=4000):
    rng = random.Random(7)
    m = [sum(rng.choice(d) for _ in d) / len(d) for _ in range(n)]
    m.sort()
    return sum(d) / len(d), m[int(.025 * n)], m[int(.975 * n)]


rows = list(zip(R["eg2"]["rows"], R["qwen3"]["rows"]))
groups = {"short (chunk <=512)": lambda q: q["Kind"] == "short",
          "long chunk, head": lambda q: q["Kind"] == "head",
          "long chunk, tail": lambda q: q["Kind"] == "tail"}
print(f"\n{'queries':22} {'strategy':9} {'n':>4} {'EG2 R@1/MRR':>13} {'Qwen3 R@1/MRR':>14}   Δ MRR [95% CI]")
for gname, f in groups.items():
    sel = [(e, q) for e, q in rows if f(e)]
    for s in (["full"] if gname.startswith("short") else ["full", "trunc512", "mean512"]):
        er = [e["Rank"][s] for e, _ in sel]
        qr = [q["Rank"][s] for _, q in sel]
        d = [rr(a) - rr(b) for a, b in zip(er, qr)]
        m, lo, hi = boot(d)
        star = "*" if lo > 0 or hi < 0 else " "
        print(f"{gname:22} {s:9} {len(sel):4} {sum(r == 1 for r in er) / len(er):6.2f}/{sum(map(rr, er)) / len(er):.2f}"
              f" {sum(r == 1 for r in qr) / len(qr):7.2f}/{sum(map(rr, qr)) / len(qr):.2f}   {m:+.3f} [{lo:+.3f}, {hi:+.3f}]{star}")
print("\nper language, long-chunk tail, mean512 vs full (MRR):")
for lang in sorted({e["Lang"] for e, _ in rows}):
    for kind in ("short", "tail"):
        sel = [(e, q) for e, q in rows if e["Lang"] == lang and e["Kind"] == kind]
        if not sel:
            continue
        line = f"  {lang} {kind:5} n={len(sel):3}"
        for s in (["full"] if kind == "short" else ["full", "mean512"]):
            line += f"  {s}: EG2 {sum(rr(e['Rank'][s]) for e, _ in sel) / len(sel):.2f} Qwen3 {sum(rr(q['Rank'][s]) for _, q in sel) / len(sel):.2f}"
        print(line)
