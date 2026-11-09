# Russian developer slang, round 2

Read [REPORT.md](../../../../bench/eg2-ru2/REPORT.md) for the verdict and evidence.
All code here is research code. The original two round-1 directories are reused
read-only. No training, local account endpoint, chat export or private row is used.

`seal.py` hashes prospective chat cases before dictionary acquisition. The same
agent authored them and ran the experiment: this is temporal/source separation,
not independent human authorship. `acquire.py` downloads entire published
Wiktionary categories and retains revisions, not a test-derived lookup list.
`build_dictionary.py` mechanically extracts computer senses and English etymons.
Dictionary data is CC BY-SA 4.0; per-entry source/history links and
`bench/eg2-ru2/sources/ATTRIBUTION.md` preserve attribution.

Post-score source audit found that 35 etymon candidates came from unresolved
generic templates, not explicit English-language markers (33 active). The frozen
extractor and scores are preserved, including this defect; see SOURCE_LIMITATIONS.md.
Do not treat every `en` field as a verified English borrowing or ship this mapping.

`pipeline2.py` preserves original text/tokens, limits vector expansion to short
Cyrillic documents (600 characters), appends at most two definitions / 240
characters, and requires a technical cue for ambiguous senses. Finite noun/verb
forms support lookup. Only documented English loans with transliteration/source
similarity >= .45 are active. Reverse lexical lookup removes frequent definition
words and requires at least two anchors and half of the remaining content words
(one for a one-word definition), takes at most two definitions, and adds missing
canonical tokens on docs and queries. This simple inverse matcher has substantial
precision/coverage limitations; see results before considering adoption. It does
not alter the base tokenizer with general stemming. Snowball is a separate arm.

Portable inspection (requires the existing round-1 `uniseg` package and NumPy):

```sh
PYTHONDONTWRITEBYTECODE=1 bench/mlx/.venv/bin/python \
  scripts/embedbench/research/eg2_ru2/pipeline2.py --role doc < approved-public.jsonl
```

Each output retains `text`, adds `embedding_text` and `lexical_tokens`. Display
the original. Never point this script at account storage without authorization.

`alias2.py` edits actual dequantized Q8 `token_embd.weight` rows in the faithful
MLX graph. Norm-preserving interpolation uses doses .25/.5/1 and matched random
row rotations. The unchanged PLE projection derives from the edited token input,
so it follows the same edit. Sparse rows are in `dev/alias_rows.npz`; the GGUF is
not modified. Token-row presence is checked on the exact prompted/truncated IDs;
absent rows are mathematical no-ops. Whole-word tokens can still be shared with
ordinary words, so this is a diagnostic requiring regression checks.

`cache2.py` reconstructs the 74,612-row public prefix from public set files and
run metadata. It never opens the old mixed `records.jsonl` or reads private vector
rows. `run2.py` reuses these and round-1 public banks; only changed text/token rows
need inference. Qwen uses its unchanged instruction/last-token loader. EG2 keeps
production prefixes, mean pooling, 2048 context, exact head and exact search.

Single-field BM25 uses NFKC/casefold/UAX#29, k1=1.2, b=.75. Hybrid uses equal
RRF(60), top 30 per leg, positive BM25 hits only. This estimates production-like
fusion; it does not run the production index. Zero-score targets are not hits.
The bootstrap joins queries connected by any relevant target before resampling,
including on public multi-relevance datasets. Metrics are MRR/nDCG/Recall@10.

Run sequence from repository root, one process at a time:

```sh
export PYTHONDONTWRITEBYTECODE=1
py=bench/mlx/.venv/bin/python
code=scripts/embedbench/research/eg2_ru2
"$py" "$code/seal.py"
"$py" "$code/acquire.py"
"$py" "$code/acquire_programming.py"
"$py" "$code/build_dictionary.py"
"$py" "$code/check2.py"
"$py" "$code/run2.py" encode dev eg2
"$py" "$code/run2.py" encode dev qwen
"$py" "$code/run2.py" score dev
"$py" "$code/freeze2.py"
"$py" "$code/confirm2.py"
"$py" "$code/validate2.py"
"$py" "$code/report2.py"
```

The delivered run also has one preserved dev-only revision: `acquire_programming.py`
adds the whole programming category before the final dictionary build/dev pass.
`dev/phase1` retains the earlier scores and dictionary. See AMENDMENTS.md; no
held-out results were used for either acquisition or rule selection.

Completed seals and scored outputs deliberately refuse overwriting. Encoding
checkpoints may resume identical inputs; verify frozen hashes before resuming.
Do not change delivered inputs to rerun a held-out test. Use a separately
authorized output tree for a new experiment. Prescore implementation repairs and
any later correctness repairs are recorded in AMENDMENTS.md / CORRECTIONS.json.
