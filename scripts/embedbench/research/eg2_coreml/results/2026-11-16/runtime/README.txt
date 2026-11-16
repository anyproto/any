Production-runtime follow-up on M2 Air, 16 GiB, macOS 27.2.1

4bc13330: binding cold, binding cached, integration all PASS. Full verbose logs are preserved.
Binding under leaks --atExit also passes its assertions; the leak tool exits 1 and reports 21 objects / 1088 bytes. This is not a zero-leak result. The stacks show five 208-byte NSMutableArray-rooted graphs allocated by Core ML/Espresso segmentation analytics during MLComputePlan, and one 48-byte xpc_date_t at Go/purego initialization. Attribution and long-run growth are not established by this one audit.
Idle timer and in-flight release regressions pass locally.

b7d2c5ee: final five-bucket pinned candidate; validation is in progress and will be added here.
Full-app 16 GiB lifecycle check is pending. No full-app 8 GiB safety result exists.

Compile probe: 574261484 logical bytes, 574271488 stat-allocated bytes. Free space decreased by 8929280 bytes on a live filesystem. This is consistent with shared/cloned storage; it is not an exact physical-extent measurement.

Log publication removes trailing whitespace only; original/published SHA256 records are in publication-record.json. No credentials or private account/network configuration are included.
