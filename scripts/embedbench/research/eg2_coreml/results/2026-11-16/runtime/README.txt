Production-runtime follow-up on M2 Air, 16 GiB, macOS 27.2.1

4bc13330: binding cold, binding cached, integration all PASS. Full verbose logs are preserved.
Binding under leaks --atExit also passes its assertions; the leak tool exits 1 and reports 21 objects / 1088 bytes. This is not a zero-leak result. The stacks show five 208-byte NSMutableArray-rooted graphs allocated by Core ML/Espresso segmentation analytics during MLComputePlan, and one 48-byte xpc_date_t at Go/purego initialization. Attribution and long-run growth are not established by this one audit. The runtime owner reviewed the complete stacks and accepted this as a small unresolved framework allocation finding; no additional audit was requested.
Idle timer and in-flight release regressions pass locally.

b7d2c5ee9002713d2e4dc9f6d0a8d8fbd9be095a: final five-bucket pinned candidate; binding and integration PASS.
Both tests used the default function list; ANY_TEST_COREML_LENGTHS was not set.
Binding: 386.592s including cold plans/loads, all16 stock-HF references, same text on all5 functions, concurrent second s2048 plan/load while s128 predicted367times, and refusal handling. Minimum stock-HF cosine0.99992672; minimum same-text-across-functions cosine0.99999664. Placement6195-6196ANE/13-14CPU, noCPU core operators.
Per-text timing for128/256/512/1024/2048:6.663/12.617/32.384/88.377/295.971ms at96/192/384/768/1536tokens. Different padded input sizes, not one common-input comparison.
Integration:370.284s including cold setup; all5 ready after6m5s. Actual runtime wrappers:10docs795msANE vs1.173sMetal; minimum cosine0.999636 over10docs+query. Both bindings and integration use the verified published package and real Q8 GGUF. Full -v outputs and exact binary hashes are included.
Full-app 16 GiB lifecycle check is pending. No full-app 8 GiB safety result exists.

Compile probe: 574261484 logical bytes, 574271488 stat-allocated bytes. Free space decreased by 8929280 bytes on a live filesystem. This is consistent with shared/cloned storage; it is not an exact physical-extent measurement.

Log publication removes trailing whitespace only; original/published SHA256 records are in publication-record.json. No credentials or private account/network configuration are included.
