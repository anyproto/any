# Token fixtures

`tokens.json.gz` is the exact compressed input used for the recorded validation:
1,500 FiQA documents, each tokenized with both document and query prompts, plus
six synthetic empty/whitespace/multilingual/special-token/long-text cases under
both prompts. There are 3,012 rows. Document `i` occupies row `i * 2`.

The FiQA text comes from the public BEIR FiQA corpus used by the embedding
benchmark. The snapshot is included so input selection and order remain fixed.
The synthetic cases exercise tokenization; they contain no account data.
All 3,012 rows matched the pinned stock tokenizer and the earlier GGUF-vocabulary
tokenization check. Source token IDs include BOS/EOS.

From the parent directory:

```sh
gzip -dc fixtures/tokens.json.gz > tokens.json
shasum -a 256 -c fixtures/SHA256SUMS
```

The decompressed JSON is ignored by Git. `SHA256SUMS` pins its uncompressed bytes;
`gzip` compression used timestamp zero for a deterministic fixture archive.
