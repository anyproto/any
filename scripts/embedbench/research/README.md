# embedbench/research

These scripts back the SYN-391 follow-up, "why EG2 trails Qwen3 and what to change" (posted to the collab chat). They cover:

- EG2's short-text bias and the query-side centering fix;
- heading-only editor windows;
- prompt and pooling variants;
- hybrid weights;
- how long chunks behave under a 512-token cap;
- the cost of token-based splitters.

Everything runs on a Mac without the account rig.

## Conventions

- **Set dir:**
  - `docs.jsonl`: `{id, title, text, body}`, where `text` is the field that gets embedded and measured.
  - `queries.jsonl`: `{id, text}`.
  - `qrels.json`: `{qid: [docid…]}`.
- **Vectors:** raw float32 `[n, dim]` with no header, in input order. Note that `TestEmbedBench` dumps carry an 8-byte header; these files don't.
- **Running the Python scripts:** scripts that import `evallib.py` must run from this dir, without `python -I`. They need numpy and tokenizers; `cml_embed.py` and `qualexp.py` also need coremltools, `bm25.py` needs `rank_bm25`, and `splitbench.py` needs `semantic-text-splitter`, `semchunk`, `chonkie` and `langchain-text-splitters`.
- **Running the Go programs:** they carry `//go:build ignore`. Run them from the repo root with `go run scripts/embedbench/research/go/<name>/main.go …`. They use the yzma llama.cpp binding and the libs in `bin/llamacpp`.

## Data

| set | how |
|---|---|
| FiQA 10k subset, SciFact, NFCorpus | download the public BEIR zips, then run `subset.py <beir dir> <set dir> <max docs>`. This mirrors `capBEIRCorpus`: every relevant doc first, then the first other docs. Use 10000 for FiQA and a large number for the full set. |
| Wikipedia window stand-in | `wiki_fetch.sh <wiki dir>` fetches 15 articles in EN/DE/RU/JA/ZH. `wikiwin.py <wiki dir> <set>` applies `window.go`'s rule: a new window before every heading and at 1,500 bytes. `wikiq.py <set>` writes 85 topical section queries. |
| Heading-only windows folded | `wikifold.py <set> <fold set>` carries a heading-only window into the next window as its first line and its `title`. |
| Russian paraphrase | `ruq.py <set> <ru set>`: 30 hand-written Russian paraphrases plus their English versions, over all windows. |
| BM25 stand-in for hybrid | `bm25.py <set> <set>/bm25.npy` |

## Embedding

- **`go/embed`:** EG2 or Qwen3 through llama.cpp on Metal, with production prompts, pooling and truncation.
  - `-query` switches to the query prompt.
  - `-title` builds `title: <title> | text:` from the doc's title.
  - `-prefix` overrides the prompt.
  - `-pool tokens` also writes per-token pooling variants: without prompt tokens, without BOS/EOS, and last-token.
- **`cml_embed.py`:** EG2 through Core ML on the Neural Engine. Set `EG2_COREML` (the compiled Nemosine `.mlmodelc`) and `EG2_TOKENIZER`. Gotcha: once two Core ML processes had overlapped, Nemosine started segfaulting (exit 139/137) on multi-bucket doc runs. Use `go/embed` for docs when that happens; its vectors match at cosine 0.9999.

## Analyses

| question | script |
|---|---|
| Does EG2 favour short docs? Hit/relevant/corpus length mix, and mean cosine by doc length | `eval_fiqa.py` |
| Cosine-vs-length slope and calibrations without labels (length bucket, log fit, centering, hubness) | `lenbias.py` |
| **Fix #1:** query-side centering `q' = norm(q − a·μ)`, μ = mean of N random doc vectors, swept over `a` and μ draws | `qcenter.py` (FiQA10k, a = 0.5, N = 100: +0.031 to +0.040 over three draws) |
| How robust centering is to where μ comes from, and to 256 dims | `centerx.py` |
| Paired A vs B with hit-length stats | `compare.py` |
| Hybrid weighting, shaped like production: fetch 30, RRF k = 60, adaptive BM25 weight | `fusion.py` (`--center` to stack it with centering) |
| Window stand-in: nDCG, share of heading-only hits, per language | `wikieval.py` (`--center`, `--drop-ho`) |
| Long chunks: full vs truncate vs segment averaging, EG2 vs Qwen3 | `lenexp_prep.py`, then `go/lenexp` (run once per model), then `lenan.py` for EG2 vs Qwen3 and `lenan2.py` for the averaging variants and centering |
| Token splitters: speed, staying under 512, where they cut, re-embeds per edit, and how the editor windows split into tokens | `splitbench.py` |
| Bucket and memory cost model from iPhone Neural Engine timings | `chunksim.py` |
| Long-block retrieval with EG2 on Core ML | `qualexp.py` |
| llama.cpp tokenizer cost and memory in Go | `go/toktime` |

## Headline results (Mac, nDCG@10 unless noted; * = 95% CI excludes 0)

- **Short-doc bias:** EG2's cosine falls by 0.0285 per tenfold increase in doc length; Qwen3's is flat. EG2 places heading-only windows at z = +0.82 against −0.08 for Qwen3.
- **Fix #1, query-side centering:** FiQA10k +0.039*, wiki +0.018*, SciFact and NFCorpus neutral.
- **Folding heading-only windows:** wiki +0.024*, and +0.032* together with fix #1.
- **Title prompt:** wiki +0.021*, NFCorpus +0.017*.
- **Segment averaging under a 512-token cap:** tail-of-chunk MRR +0.096*. With centering, overall MRR +0.018*.
- **EG2 vs Qwen3 on long chunks:** EG2 wins, 0.73 vs 0.51 tail MRR at full length.
