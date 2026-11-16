Production-runtime follow-up on M2 Air, 16 GiB, macOS 27.2.1

4bc13330: binding cold, binding cached, integration all PASS. Full verbose logs are preserved.
Binding under leaks --atExit also passes its assertions; the leak tool exits 1 and reports 21 objects / 1088 bytes. This is not a zero-leak result. The stacks show five 208-byte NSMutableArray-rooted graphs allocated by Core ML/Espresso segmentation analytics during MLComputePlan, and one 48-byte xpc_date_t at Go/purego initialization. Attribution and long-run growth are not established by this one audit. The runtime owner reviewed the complete stacks and accepted this as a small unresolved framework allocation finding; no additional audit was requested.
Idle timer and in-flight release regressions pass locally.

b7d2c5ee9002713d2e4dc9f6d0a8d8fbd9be095a: final five-bucket pinned candidate; binding and integration PASS.
Both tests used the default function list; ANY_TEST_COREML_LENGTHS was not set.
Binding: 386.592 s including cold plans/loads, all 16 stock-HF references, same text on all 5 functions, concurrent second s2048 plan/load while s128 predicted 367 times, and refusal handling. Minimum stock-HF cosine 0.99992672; minimum same-text-across-functions cosine 0.99999664. Placement 6195–6196 ANE / 13–14 CPU operations, no CPU core operators.
Per-text timing for 128/256/512/1024/2048: 6.663/12.617/32.384/88.377/295.971 ms at 96/192/384/768/1536 tokens. Different padded input sizes, not one common-input comparison.
Integration: 370.284 s including cold setup; all 5 ready after 6m5s. Actual runtime wrappers:10 docs, 795 ms ANE vs 1.173 s Metal; minimum cosine 0.999636 over 10 docs + query. Both bindings and integration use the verified published package and real Q8 GGUF. Full -v outputs and exact binary hashes are included.
Full-app 16 GiB lifecycle check: PASS on the exact b7d2c5ee binary. See fullapp-b7d2c5ee/REPORT.txt and the raw ledgers.
Fresh staging account and empty model cache; public GGUF and all three Core ML files downloaded by the real app and hashes verified. Download process observations show one download at a time. All five functions became ready, ten synthetic vector HTTP requests passed. Test settings: contextSize 2048, batchDocs 1, embedConcurrency 1, embedBatch 8.
No embedding requests or writes during idle: s256/s512/s1024/s2048 released after about 60 s, child retired after about 300 s and was absent at 315 s. Shutdown exited 0; temporary credentials removed; no owned children left.
Active ordinary footprint: app 127 MB, child 778 MB. Separate neural ledger: wired 2031 MB, reclaimable 1047 MB. At 65 s: app 127 MB, child 635 MB; neural wired 0 B, reclaimable 2991 MB. At 315 s: app 112 MB, child absent. Values are footprint's displayed units. Reclaimable cache is not already freed, and overlapping columns must not be summed into an exact total. One active snapshot is not a peak bound.
No full-app 8 GiB safety result exists. Exact specialization-cache invalidation factors remain unmeasured. This run validates synthetic flow/lifetime, not retrieval quality or later runtime commits.

Compile probe: 574261484 logical bytes, 574271488 stat-allocated bytes. Free space decreased by 8929280 bytes on a live filesystem. This is consistent with shared/cloned storage; it is not an exact physical-extent measurement.

Log publication removes trailing whitespace only; original/published SHA256 records are in publication-record.json. No credentials or private account/network configuration are included.
