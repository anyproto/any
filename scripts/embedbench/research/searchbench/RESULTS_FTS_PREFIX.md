# FTS rewrites for typos and search-as-you-type (Roma's snapshot, Qwen3 vector leg from the original query, MRR)

Typo rewrite: every word cut to its first max(3, min(5, len-2)) chars + `*`. Prefix rewrite: `*` on the unfinished last word.

## typo (102)

| variant | MRR |
|---|---|
| FTS original | 0.000 |
| FTS rewritten | 0.113 |
| fusion w1, original FTS | 0.310 |
| fusion w1, rewritten FTS | 0.326 |
| fusion w1, fallback (rewrite only if FTS empty) | 0.326 |
| vector only | 0.310 |

## prefix (76)

| variant | MRR |
|---|---|
| FTS original | 0.581 |
| FTS rewritten | 0.710 |
| fusion w1, original FTS | 0.663 |
| fusion w1, rewritten FTS | 0.723 |
| fusion w1, fallback (rewrite only if FTS empty) | 0.663 |
| vector only | 0.609 |

