# Hybrid fusion sweep on a real index (SYN-391)

Runs the production hybrid `Search` over a copy of an existing `index.db` at several vector weights. For each query it
dumps the title block and both content legs at chunk level, using the same windows as `Search` at limit 10. That allows
offline re-fusion and other variants.

## Files

- `fusion_sweep_test.go.txt`: drop it into `internal/indexer/` as `fusion_sweep_test.go` at the commit the app runs.
  The 2026-11-10 run used any `97c978cf` (Any Nightly v0.3.8). Build with `-tags llamacpp`. It uses unexported indexer
  functions, so it only compiles inside the package.
- `analyze.py`:
  - reproduction check: offline re-fusion against the production page at every weight;
  - page sensitivity;
  - known-item MRR / hit@1 / recall@10 per query shape, with paired bootstrap;
  - which leg found the target.
- `analyze2.py`: a finer weight grid, the share of content slots held by records found by one leg only, and a convex
  blend of min-max-normalized leg scores.
- `analyze3.py` (with `common.py`): the blend's alpha chosen by repeated 2-fold CV over targets.

## Run

```sh
go test -tags llamacpp -c -o indexer.test ./internal/indexer
SWEEP_DB=<index.db copy> SWEEP_QUERIES=queries.jsonl SWEEP_OUT=sweep.jsonl \
SWEEP_MODEL=<the app's GGUF> SWEEP_LIBDIR=<the app's llama.cpp dir> \
SWEEP_QUERY_PREFIX=$'Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:' \
SWEEP_ANY_BIN=<the app's any binary> ./indexer.test -test.run 'TestFusionSweep$' -test.v
```

**Inputs.** `queries.jsonl` has one `{"Qid": "<tid>-q1|q2|q3", "Space": "<spaceId>", "Query": "..."}` per line. The
directory also needs `targets.json` (`tid`, `objectId`, ...). Then:

```sh
python3 analyze.py <dir> [weak tids]
SWEEP_DIR=<dir> python3 analyze2.py <dir>
SWEEP_DIR=<dir> python3 analyze3.py
```

**Copying the index.** Copy `index.db` from a quiet moment, or use an `any-store-cli2` built at the store's version. An
old CLI (v2.0.0-alpha) reports "database is corrupt" on the v2.4 format.
