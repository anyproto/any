"""Embed public sets with LiteRT-LM's int4 EG2 text-270m (CPU), production prompts, and save per-set vectors.
python embed_sets.py <model.litertlm> <eg2-patch dir> <out dir> <set,set,...> [limit]"""
import sys, json, time, os
import numpy as np
import litert_lm as L
M, B, OUT, SETS = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4].split(',')
LIM = int(sys.argv[5]) if len(sys.argv) > 5 else 0
eng = L.EmbeddingEngine(M, backend=(L.Backend.GPU() if os.environ.get('BACKEND') == 'gpu' else L.Backend.CPU()), cache_dir=os.path.join(OUT, 'cache'), max_input_length=2048)
opt = L.EmbeddingOptions(normalize=True, input_overflow_strategy=L.InputOverflowStrategy.TRUNCATE)
P = {'doc': 'title: none | text: ', 'query': 'task: search result | query: '}
for s in SETS:
    d = os.path.join(OUT, s); os.makedirs(d, exist_ok=True)
    for role, fn in (('doc', 'corpus.jsonl'), ('query', 'queries.jsonl')):
        rows = [json.loads(l) for l in open(f'{B}/{s}/{fn}') if l.strip()]
        rows = [dict(id=r.get('r') or r.get('Qid'), text=r.get('text') or r.get('Query')) for r in rows]
        if LIM: rows = rows[:LIM]
        V = np.zeros((len(rows), 768), np.float32); t0 = time.time()
        for i, r in enumerate(rows):
            V[i] = eng.compute_embedding(P[role] + r['text'], opt).embedding
        dt = time.time() - t0
        np.save(f'{d}/{role}.npy', V)
        with open(f'{d}/{role}.texts.jsonl', 'w') as o:
            for r in rows: o.write(json.dumps({'id': r['id'], 'text': r['text']}, ensure_ascii=False) + '\n')
        print(f'{s} {role}: {len(rows)} in {dt:.1f}s ({1000 * dt / max(1, len(rows)):.1f} ms each)', flush=True)
