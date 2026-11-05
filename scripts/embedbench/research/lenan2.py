"""lenan2.py <lenexp>.eg2.ranks.json: MRR@10 per query kind (short / head /
tail / all) for every lenexp strategy, with the paired-bootstrap delta vs
"full" (today's full-length embedding)."""
import json, random, sys

rows = json.load(open(sys.argv[1]))["rows"]
rr = lambda r: 1 / r if r <= 10 else 0.0


def boot(x, n=4000):
    rng = random.Random(7)
    m = sorted(sum(rng.choice(x) for _ in x) / len(x) for _ in range(n))
    return sum(x) / len(x), m[int(.025 * n)], m[int(.975 * n)]


S = ["full", "trunc512", "mean512", "mean512eq", "full+c", "mean512+c", "mean512eq+c"]
print(f"{'strategy':13}" + "".join(f"{k:>24}" for k in ["short", "head", "tail", "all"]))
for s in S:
    line = f"{s:13}"
    for kind in ["short", "head", "tail", "all"]:
        sel = [r for r in rows if kind == "all" or r["Kind"] == kind]
        m = sum(rr(r["Rank"][s]) for r in sel) / len(sel)
        mm, lo, hi = boot([rr(r["Rank"][s]) - rr(r["Rank"]["full"]) for r in sel])
        star = "*" if lo > 0 or hi < 0 else " "
        line += f"  {m:.3f} ({mm:+.3f}{star})" if s != "full" else f"  {m:.3f}          "
    print(line)
print("cells: MRR@10 (delta vs full, * = 95% CI excludes 0)")
