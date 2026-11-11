# Typo correction against the index vocabulary

Each query word missing from the space's vocabulary is replaced by the most frequent vocabulary word at edit distance 1 (2 for 8+ chars). No index change. Production Qwen3 legs from the sweep. MRR@10 (M0 label rule / AirM article level).

| set | n (changed) | FTS orig → corrected | vector only orig → corrected | fused w1 orig → both legs corrected | fused w1, corrected FTS + original vector leg |
|---|---|---|---|---|---|
| m0 all | 102 (57) | 0.000 → 0.292 | 0.310 → 0.354 | 0.310 → 0.416 | 0.402 |
| m0 changed only | 57 (57) | 0.000 → 0.523 | 0.401 → 0.480 | 0.401 → 0.592 | 0.566 |
| airm all | 88 (77) | 0.000 → 0.408 | 0.103 → 0.226 | 0.103 → 0.397 | 0.367 |
| airm changed only | 77 (77) | 0.000 → 0.467 | 0.101 → 0.242 | 0.101 → 0.438 | 0.403 |


Of the 117 typo queries the correction changed (those with a one-word source), it recovered the intended word in 106 and picked a different word in 11.
