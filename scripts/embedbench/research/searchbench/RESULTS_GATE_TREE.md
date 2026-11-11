# Gate threshold sweep under the RRF-only tree (Roma's M1 judged queries, Qwen3)

126 queries (benchmark lookups in 9 shapes, not real recall prompts). Each query's own tree page, content hits only. 578/579 of the top-5 content hits are judged; unjudged hits count as injected but are left out of the relevant share. Relevant = grade ≥ 2 of 3.

Branch mix: lexical-heavy 42 (33%), vector-heavy 42 (33%), FTS only 33 (26%), vector only 9 (7%)

| floor | cap 3: injected / query | relevant | queries injecting | cap 5: injected / query | relevant | queries injecting |
|---|---|---|---|---|---|---|
| 0.6 | 2.76 | 51% | 100% | 4.02 | 46% | 100% |
| 0.65 | 2.42 | 55% | 100% | 3.52 | 50% | 100% |
| 0.7 | 2.18 | 60% | 87% | 3.06 | 54% | 87% |
| 0.75 | 2.07 | 62% | 85% | 2.90 | 55% | 85% |
| 0.8 | 1.91 | 64% | 83% | 2.60 | 57% | 83% |
| 0.85 | 1.75 | 66% | 83% | 2.27 | 59% | 83% |
| 0.9 | 1.46 | 70% | 76% | 1.89 | 61% | 76% |
| 0.95 | 1.12 | 72% | 64% | 1.29 | 69% | 64% |
| 0.915 | 1.37 | 71% | 75% | 1.77 | 63% | 75% |

## By branch (cap 3)

| branch (queries) | floor 0.80: injected / relevant | 0.90 | 0.95 | top content score, median |
|---|---|---|---|---|
| vector-heavy (42) | 1.43 / 58% | 0.79 / 67% | 0.38 / 81% | 0.936 |
| lexical-heavy (42) | 1.90 / 69% | 1.21 / 82% | 0.71 / 87% | 0.958 |
| FTS only (33) | 2.24 / 66% | 2.24 / 66% | 2.18 / 67% | 1.000 |
| vector only (9) | 3.00 / 56% | 2.89 / 58% | 2.56 / 65% | 1.000 |

## Single-leg branches (FTS only, vector only): the score is 61 / (61 + rank)

| branch | rank 1 relevant | rank 2 | rank 3 | rank 4 | rank 5 |
|---|---|---|---|---|---|
| FTS only | 91% (33) | 48% (21) | 45% (20) | 35% (20) | 32% (19) |
| vector only | 100% (9) | 33% (9) | 33% (9) | 33% (9) | 11% (9) |

## Alternatives for single-leg hits (cap 3)

| floor | as decided: injected / relevant / relevant per query | single-leg on the two-leg scale (rank 1 = 0.5) | single-leg: rank 1 only |
|---|---|---|---|
| 0.8 | 1.91 / 64% / 1.22 | 1.11 / 64% / 0.71 | 1.44 / 71% / 1.02 |
| 0.85 | 1.75 / 66% / 1.15 | 0.95 / 68% / 0.64 | 1.29 / 74% / 0.95 |
| 0.9 | 1.46 / 70% / 1.02 | 0.67 / 76% / 0.51 | 1.00 / 82% / 0.82 |
| 0.915 | 1.37 / 71% / 0.98 | 0.59 / 80% / 0.47 | 0.92 / 84% / 0.78 |
| 0.95 | 1.12 / 72% / 0.81 | 0.37 / 85% / 0.31 | 0.70 / 89% / 0.62 |
