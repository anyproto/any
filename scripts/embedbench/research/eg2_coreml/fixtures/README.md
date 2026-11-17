# Evaluation fixtures

`tokens.json.gz` contains the exact 3,012 rows used in the recorded validation:
the first 1,500 non-empty BEIR FiQA corpus documents in archive order, each with
a document and a search-query prompt, plus six synthetic edge cases under both
prompts. Document `i` occupies rows `2*i` and `2*i+1` (zero-based).

These fixtures are **CC-BY-SA-4.0**, separately from the Apache-2.0 model and
converter. Credit: FiQA-2018 contributors and BEIR dataset maintainers.
Retain the [full attribution and modifications notice](../licenses/FIQA-NOTICE.txt)
when redistributing the fixture or its text. The [licence text](../licenses/CC-BY-SA-4.0.txt)
is included; it is also available at <https://creativecommons.org/licenses/by-sa/4.0/>.
Source: <https://huggingface.co/datasets/BeIR/fiqa> and the original
[FiQA task](https://sites.google.com/view/fiqa/).

[source-map.json](source-map.json) maps every corpus document to its source ID,
archive line and text hash, and identifies the FiQA texts in the two reference
JSON files. The original corpus does not provide per-post authors. Corpus text,
links, token IDs, row order and validation results remain unchanged.

From the conversion directory:

```sh
gzip -dc fixtures/tokens.json.gz > tokens.json
shasum -a 256 -c fixtures/SHA256SUMS
```

The checksum pins the decompressed JSON. Gzip timestamp zero makes the archive
deterministic. The source-map and licence additions do not change these bytes.
