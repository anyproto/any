import sys, json, time
import numpy as np
import litert_lm as L
M, B, OUT = sys.argv[1], sys.argv[2], sys.argv[3]
eng = L.EmbeddingEngine(M, backend=L.Backend.GPU(), cache_dir=f'{OUT}/cache', max_input_length=2048)
opt = L.EmbeddingOptions(normalize=True, input_overflow_strategy=L.InputOverflowStrategy.TRUNCATE)
T = [json.loads(l)['text'] for l in open(f'{OUT}/ru/doc.texts.jsonl')]
C = np.load(f'{OUT}/ru/doc.npy'); ts = []; G = []
for t in T:
    a = time.time(); G.append(eng.compute_embedding('title: none | text: ' + t, opt).embedding); ts.append(time.time() - a)
G = np.array(G, np.float32)
print(f'GPU: first {ts[0]*1000:.0f} ms, median {np.median(ts[1:])*1000:.0f} ms; cos GPU vs CPU mean {(G*C).sum(1).mean():.5f} min {(G*C).sum(1).min():.5f}')
