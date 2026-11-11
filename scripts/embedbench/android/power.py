import sys, re, numpy as np
rows = []
for l in open(sys.argv[1]):
    m = re.match(r'(\d+) 00000000 00000000 00000001 ([0-9a-f]{8})\s+([0-9a-f]{8})?.*mv=(\d+)', l)
    if not m: continue
    lo = int(m[2], 16); v = (0xffffffff << 32 | lo) - (1 << 64) if lo & 0x80000000 else lo  # sign-extend the low word
    rows.append((int(m[1]), -v / 1e6, int(m[4]) / 1000))   # A discharging (positive), V
R = np.array(rows)
def win(a, b):
    s = R[(R[:, 0] >= a) & (R[:, 0] <= b)]; return s, (s[:, 1] * s[:, 2]).mean() if len(s) else float('nan')
for spec in sys.argv[2:]:
    name, a, b, it = spec.split(':'); s, w = win(int(a), int(b))
    print(f'{name}: {len(s)} samples, mean {s[:,1].mean()*1000:.0f} mA, {w:.2f} W gross, {w * (int(b) - int(a)) / int(it) if int(it) else 0:.2f} J per embedding gross')
