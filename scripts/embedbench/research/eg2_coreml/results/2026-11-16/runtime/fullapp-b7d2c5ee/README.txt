Full-app evidence — 16 November 2026

Start with REPORT.txt and SUMMARY.json. result.json records the executed harness result. This is a synthetic mixed-length application-flow and lifecycle test on the 16 GiB M2 Air; it does not measure retrieval quality or establish an 8 GiB memory bound.

The tested app is b7d2c5ee9002713d2e4dc9f6d0a8d8fbd9be095a. The public Core ML package is anylab/embeddinggemma-2-coreml at c27f9012633055fea5bb47fe782ab7d548d77909. downloaded-package.json and downloaded-gguf.json contain actual downloaded file hashes. model-package.json is the expected contract. executed-artifacts.json pins the binary, exact executed harness and synthetic inputs; the binary and model weights are not included.

harness.py is exactly the executed file, including its original formatting. It runs with Python 3.9.6 on the Air. It creates a new identity in a fresh 0700 directory, uses a loopback ephemeral port and the supplied real staging node configuration, and removes temporary credentials and account SDK state after shutdown. Real staging configuration is required and is intentionally not included. P2P/discovery/push are disabled; this is not an offline-network claim.

Example on an authorized staging test host, using an existing b11600 llama.cpp library directory and an exact b7d2c5ee app binary built with the llamacpp tag:
  python3 harness.py --root /tmp/ane-fullapp-fresh-run --binary /path/to/any --nodeconf /private/path/to/staging.yml --lib-dir /path/to/llamacpp-b11600
Run only when the host has no other inference owner. inputs.json and model-package.json must remain adjacent to harness.py. The run root must not exist. The harness performs actual public downloads and network activity against staging; it is not an offline summary command.

To recheck only the already collected evidence (no app or inference):
  python3 summarize_fullapp.py --evidence .
  shasum -a 256 -c SHA256SUMS
summarize_fullapp.py needs Python 3.12 and only its standard library. It independently checks the quiet interval against lifecycle timestamps, validates download order and successful result flags, then regenerates SUMMARY.json and REPORT.txt. Do not mutate the collected evidence before checking its manifest.

Download history is retained as 89 changed /v1/processes snapshots in processes-download.jsonl; processes-download-latest.json is the last observed snapshot. app.log includes only local-embedder, neural-engine and model-download lifecycle lines. The full raw log remains in the private task directory and is not included. collect_shareable.py documents the filtering/redaction and requires PyYAML plus the private staging configuration when run on raw evidence; it is not needed to review this bundle. No account authentication, recovery phrase, private node configuration or endpoint inventory is included.

During lazy startup, warmup queries may use the Metal fallback. The ten retained http-vector-timing.json requests are the final two rounds after all five functions are ready and indexing has quiesced. Each request asserted vectorStatus=used, finite result scores and at least one created-page hit. Same-topic synthetic hits establish flow, not ranking quality. See events.jsonl for startup and final-query timing.

footprint-*.txt are the original ledgers, except private staging values are redacted if present. Ordinary parent/child physical footprint is separate from neural nofootprint Clean/Reclaimable/Wired columns. These columns overlap and must not be summed as exact total RAM; reclaimable is not already freed. One active snapshot is not a peak bound. snapshot-*.json records child presence at active, 65-second and 315-second checkpoints. Final shutdown exited 0, no owned child survived, and disposable credentials/SDK state were removed. The public cache remains on the Air.
