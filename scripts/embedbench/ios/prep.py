"""prep.py <tokenizer.json> <corpus.jsonl> <outdir> [--no-prompts]
Writes tokens.bin (HF token ids with the EG2 bench prompts, or none for Granite; untruncated)
and corpus.json (raw doc texts + the 16 bench queries) for the iPhone app.
tokens.bin: b"TOK1", u32 nDocs, u32 nQueries, then per text u32 len + i32 ids."""
import json, struct, sys
from tokenizers import Tokenizer
DOC = "title: none | text: "
QRY = "task: search result | query: "
QUERIES = [
    "bird strike forced landing on a river",
    "which console had the best selling handheld",
    "how does the indexer handle deleted records",
    "почему синхронизация тормозит после логина",
    "push notification keys for the receiver",
    "engine failure after takeoff caused by maintenance error",
    "memory leak in subscription snapshots",
    "как настроить локальный эмбеддер на GPU",
    "release process for the sdk tags",
    "midair collision over Europe",
    "what did we decide about chat read markers",
    "Sega cartridge based 16-bit system",
    "ошибка при открытии базы данных",
    "vector index recall and IVF probes",
    "pilot error in bad weather approach",
    "the type and collection object model",
]
if "--no-prompts" in sys.argv:
    sys.argv.remove("--no-prompts")
    DOC = QRY = ""
tok = Tokenizer.from_file(sys.argv[1])
docs = [json.loads(l)["text"] for l in open(sys.argv[2])]
out = sys.argv[3]
with open(out + "/tokens.bin", "wb") as f:
    f.write(b"TOK1" + struct.pack("<II", len(docs), len(QUERIES)))
    for t in [DOC + d for d in docs] + [QRY + q for q in QUERIES]:
        ids = tok.encode(t, add_special_tokens=True).ids
        f.write(struct.pack("<I", len(ids)) + struct.pack("<%di" % len(ids), *ids))
json.dump({"docs": docs, "queries": QUERIES}, open(out + "/corpus.json", "w"), ensure_ascii=False)
print("eos", tok.token_to_id("<eos>"), "docs", len(docs))
