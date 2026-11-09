"""Retrieval check of published 4-bit Qwen3-Embedding-0.6B builds against production Q8_0.

q4_eval.py parity            # MLX-dequantized Q4_K_M vs llama.cpp Q4_K_M vectors, and Q8 cache parity
q4_eval.py encode <arm>      # arm: q8 | q4km | dwq | q4km-imat | q5km; caches vectors under bench/qwen3-q4/vectors/<arm>/
q4_eval.py score             # vector-only exact search, Δ vs q8 with paired cluster bootstrap

Every arm runs the same MLX Qwen3 graph as eg2_patch/qwen.py (Instruct query prefix, bare docs,
last-token pooling, L2 norm, 2048 context). q4km loads the enacimie Q4_K_M GGUF dequantized with
gguf-py, so its weights are exactly the 4-bit ones; dwq runs the mlx-community 4-bit DWQ checkpoint
natively quantized (group 64). Public data and rig-cleared look-alikes only.
"""
import sys, json, time
from pathlib import Path
HERE = Path(__file__).resolve().parent
sys.path[:0] = [str(HERE.parent / 'eg2_patch'), str(HERE.parent / 'eg2_ru2')]
import numpy as np

ROOT = HERE.parents[3]
OUT = ROOT / 'bench/qwen3-q4'
GGUFS = {'q4km': OUT / 'enacimie/Qwen3-Embedding-0.6B-Q4_K_M-GGUF/qwen3-embedding-0.6b-q4_k_m.gguf',
         'q4km-imat': OUT / 'PeterAM4/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q4_K_M-imat.gguf',
         'q5km': OUT / 'PeterAM4/Qwen3-Embedding-0.6B-GGUF/Qwen3-Embedding-0.6B-Q5_K_M.gguf'}
ARMS = ['q8', 'q4km', 'dwq', 'q4km-imat', 'q5km']
DWQ = OUT / 'mlx-community/Qwen3-Embedding-0.6B-4bit-DWQ'
PATCH_SETS = ['ru', 'miracl-ru-pool', 'miracl-de-pool', 'scifact', 'nfcorpus', 'wiki-mix', 'rig-known', 'rig-topical']
RU2_SETS = ['sealed-ru', 'new-rig']
SETS = RU2_SETS + PATCH_SETS
METRICS = ['mrr10', 'ndcg10', 'recall10']


def read(p):
    return [json.loads(s) for s in open(p) if s.strip()]


def dataset(name):
    if name in RU2_SETS:
        import data2
        return data2.dataset(name)
    p = ROOT / 'bench/eg2-patch/sets' / name
    ds = sorted(read(p / 'docs.jsonl'), key=lambda r: r['id'])
    return ds, read(p / 'queries.jsonl'), json.loads((p / 'qrels.json').read_text())


def model(arm):
    import mlx.core as mx
    from qwen import Qwen
    if arm == 'q8':
        return Qwen()
    m = Qwen.__new__(Qwen)
    from mlx_lm.models import qwen3
    from tokenizers import Tokenizer
    hf = ROOT / 'bench/mlx/qwen3-emb-0.6b'
    cfg = json.loads((hf / 'config.json').read_text())
    m.model = qwen3.Model(qwen3.ModelArgs.from_dict(cfg))
    if arm in GGUFS:
        import gguf
        r = gguf.GGUFReader(str(GGUFS[arm]))
        names = {'token_embd': 'model.embed_tokens', 'output_norm': 'model.norm'}
        layers = {'attn_q': 'self_attn.q_proj', 'attn_k': 'self_attn.k_proj', 'attn_v': 'self_attn.v_proj',
                  'attn_output': 'self_attn.o_proj', 'attn_q_norm': 'self_attn.q_norm', 'attn_k_norm': 'self_attn.k_norm',
                  'attn_norm': 'input_layernorm', 'ffn_norm': 'post_attention_layernorm', 'ffn_gate': 'mlp.gate_proj',
                  'ffn_up': 'mlp.up_proj', 'ffn_down': 'mlp.down_proj'}
        w, types = [], {}
        for t in r.tensors:
            part = t.name.split('.')
            name = (f'model.layers.{part[1]}.' + layers[part[2]]) if part[0] == 'blk' else names[part[0]]
            types[t.tensor_type.name] = types.get(t.tensor_type.name, 0) + 1
            w.append((name + '.weight', mx.array(gguf.dequantize(t.data, t.tensor_type).copy()).astype(mx.float16)))
        print(arm, 'tensor types', types, flush=True)
        m.model.load_weights(w, strict=False)
    elif arm == 'dwq':
        import mlx.nn as nn
        q = json.loads((DWQ / 'config.json').read_text())['quantization']
        nn.quantize(m.model, group_size=q['group_size'], bits=q['bits'])
        w = mx.load(str(DWQ / 'model.safetensors'))
        m.model.load_weights([(k, v if v.dtype == mx.uint32 else v.astype(mx.float16)) for k, v in w.items()], strict=False)
    else:
        raise SystemExit(f'unknown arm {arm}')
    m.model.eval(); mx.eval(m.model.parameters())
    m.tok = Tokenizer.from_file(str(hf / 'tokenizer.json'))
    return m


def vec_path(arm, name, role):
    return OUT / 'vectors' / arm / f'{name}.{role}.npy'


def norm(x):
    return x / np.maximum(np.linalg.norm(x, axis=-1, keepdims=True), 1e-12)


def encode(arm):
    m = model(arm)
    for name in SETS:
        ds, qs, _ = dataset(name)
        for role, rows in [('doc', ds), ('query', qs)]:
            p = vec_path(arm, name, role)
            if p.exists():
                continue
            t = time.time()
            v = m.encode([dict(text=r['text'], role=role) for r in rows], progress=False)
            p.parent.mkdir(parents=True, exist_ok=True); np.save(p, v.astype(np.float32))
            print(f'{arm} {name} {role}: {len(rows)} in {time.time()-t:.0f}s', flush=True)


def parity():
    """Same-weights check of the MLX graph against llama.cpp, on the first 200 FiQA-1500 docs."""
    import struct
    texts = [json.loads(l)['text'] for l in open(ROOT / 'bench/fiqa-1500.jsonl')][:200]

    def rd(p):
        b = open(p, 'rb').read(); n, d = struct.unpack('<II', b[:8])
        return norm(np.frombuffer(b[8:], '<f4').reshape(n, d))
    for arm, ref in [('q4km', OUT / 'out/gguf-q4km-metal.docs.f32'), ('q8', ROOT / 'bench/qwen3-q8-metal-b11600.docs.f32')]:
        v = norm(model(arm).encode([dict(text=t, role='doc') for t in texts], progress=False))
        c = (v * rd(ref)[:200]).sum(1)
        print(f'parity {arm} MLX vs llama.cpp Metal, 200 docs: mean {c.mean():.6f} min {c.min():.6f}', flush=True)


def metrics(scores, ds, qs, rels):
    order = np.argsort(-scores, axis=1, kind='stable')[:, :10]; ids = [r['id'] for r in ds]; nd, rr, rec = [], [], []
    disc = 1 / np.log2(np.arange(10) + 2)
    for i, q in enumerate(qs):
        rel = rels[q['id']]; rel = dict.fromkeys(rel, 1) if isinstance(rel, list) else rel
        gain = np.array([2 ** rel.get(ids[j], 0) - 1 for j in order[i]])
        ideal = sorted([2 ** g - 1 for g in rel.values() if g > 0], reverse=True)[:10]
        nd.append(float(gain @ disc / (np.asarray(ideal) @ disc[:len(ideal)])) if ideal else 0.)
        hit = np.flatnonzero(gain > 0); rr.append(1 / (hit[0] + 1) if len(hit) else 0.)
        rec.append(len(hit) / max(1, sum(g > 0 for g in rel.values())))
    return dict(mrr10=np.array(rr), ndcg10=np.array(nd), recall10=np.array(rec))


def score():
    from util2 import ci
    from data2 import clusters
    arms = [a for a in ARMS if all(vec_path(a, n, 'query').exists() for n in SETS)]
    res, lines = {}, ['| set | n q | metric | Q8 | ' + ' | '.join(f'{a} | Δ {a} − Q8 [95% CI]' for a in arms[1:]) + ' |',
                      '|' + '---|' * (4 + 2 * len(arms[1:]))]
    for name in SETS:
        ds, qs, rels = dataset(name); g = clusters(qs, rels)
        ms = {a: metrics(norm(np.load(vec_path(a, name, 'query'))) @ norm(np.load(vec_path(a, name, 'doc'))).T, ds, qs, rels) for a in arms}
        cos = {a: float((norm(np.load(vec_path(a, name, 'doc'))) * norm(np.load(vec_path('q8', name, 'doc')))).sum(1).mean()) for a in arms[1:]}
        res[name] = dict(n=len(qs), docs=len(ds), doc_cos_vs_q8=cos, means={a: {k: float(m[k].mean()) for k in METRICS} for a, m in ms.items()},
                         delta={a: {k: ci(ms[a][k], ms['q8'][k], g) for k in METRICS} for a in arms[1:]})
        for k in ['ndcg10', 'mrr10']:
            cells = []
            for a in arms[1:]:
                d = res[name]['delta'][a][k]; star = '*' if d['lo'] > 0 or d['hi'] < 0 else ''
                cells += [f"{res[name]['means'][a][k]:.3f}", f"{d['delta']:+.3f}{star} [{d['lo']:+.3f}, {d['hi']:+.3f}]"]
            lines.append(f"| {name} | {len(qs)} | {k} | {res[name]['means']['q8'][k]:.3f} | " + ' | '.join(cells) + ' |')
    (OUT / 'results.json').write_text(json.dumps(res, indent=2)); (OUT / 'RESULTS.md').write_text('\n'.join(lines) + '\n')
    print('\n'.join(lines))


if __name__ == '__main__':
    cmd = sys.argv[1]
    {'parity': parity, 'score': score}.get(cmd, lambda: encode(sys.argv[2]))()
