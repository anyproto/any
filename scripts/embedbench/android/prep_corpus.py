"""fiqa-1500.jsonl + the 16 TestEmbedBench queries -> NUL-separated docs.bin / queries.bin with production prompts.
python prep_corpus.py <fiqa-1500.jsonl> <out dir> [repeat]"""
import sys, json
src, out = sys.argv[1], sys.argv[2]
Q = ["bird strike forced landing on a river", "which console had the best selling handheld", "how does the indexer handle deleted records",
     "почему синхронизация тормозит после логина", "push notification keys for the receiver", "engine failure after takeoff caused by maintenance error",
     "memory leak in subscription snapshots", "как настроить локальный эмбеддер на GPU", "release process for the sdk tags", "midair collision over Europe",
     "what did we decide about chat read markers", "Sega cartridge based 16-bit system", "ошибка при открытии базы данных",
     "vector index recall and IVF probes", "pilot error in bad weather approach", "the type and collection object model"]
docs = [json.loads(l)['text'] for l in open(src) if l.strip()]
assert all('\0' not in d for d in docs)
open(f'{out}/docs.bin', 'wb').write(b'\0'.join(('title: none | text: ' + d).encode() for d in docs) + b'\0')
open(f'{out}/queries.bin', 'wb').write(b'\0'.join(('task: search result | query: ' + q).encode() for q in Q) + b'\0')
print(len(docs), 'docs,', sum(map(len, docs)), 'chars;', len(Q), 'queries')
