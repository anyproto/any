import sys, json, time, os
import litert_lm as L
M = sys.argv[1]; B = sys.argv[2]
docs = [json.loads(l)['text'] for l in open(f'{B}/sets/ru/docs.jsonl')][:12]
for thr, mx in [(4, 128), (4, 256), (None, 128)]:
    be = L.Backend.CPU(thread_count=thr) if thr else L.Backend.CPU()
    t0 = time.time(); eng = L.EmbeddingEngine(M, backend=be, cache_dir=sys.argv[3], max_input_length=mx); tl = time.time() - t0
    opt = L.EmbeddingOptions(normalize=True, input_overflow_strategy=L.InputOverflowStrategy.TRUNCATE)
    ts = []
    for d in docs:
        t = time.time(); eng.compute_embedding('title: none | text: ' + d, opt); ts.append(time.time() - t)
    q = []
    for d in docs:
        t = time.time(); eng.compute_embedding('task: search result | query: ' + d[:40], opt); q.append(time.time() - t)
    print(f'threads={thr} max={mx}: load {tl:.1f}s; doc first {ts[0]*1000:.0f} ms, rest median {sorted(ts[1:])[len(ts)//2]*1000:.0f} ms; short query first {q[0]*1000:.0f} ms, rest median {sorted(q[1:])[len(q)//2]*1000:.0f} ms', flush=True)
    del eng
