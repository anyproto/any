# Router F2: sensitivity to the low (vector-only) cut

F2 = full coverage → min-max blend α0.3; coverage < cut → vector leg only; otherwise RRF w2. Cut 0 = two branches only. Δ against RRF w1; * = 95% CI excludes 0 (M0 clustered by target).

| variant | M0 qwen | M0 eg2 | public qwen | public eg2 |
|---|---|---|---|---|
| w2 | 0.613 (-0.052*) | 0.629 (-0.050*) | 0.618 (+0.012*) | 0.600 (+0.003) |
| F2 low cut 0.75 | 0.706 (+0.041*) | 0.703 (+0.024*) | 0.653 (+0.047*) | 0.635 (+0.039*) |
| F2 low cut 0.5 | 0.709 (+0.043*) | 0.707 (+0.029*) | 0.630 (+0.024*) | 0.617 (+0.020*) |
| F2 low cut 0.25 | 0.706 (+0.040*) | 0.704 (+0.026*) | 0.623 (+0.017*) | 0.607 (+0.010*) |
| F2 low cut 0.0 | 0.706 (+0.040*) | 0.704 (+0.025*) | 0.620 (+0.014*) | 0.604 (+0.007*) |
