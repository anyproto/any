from util import *
assert not (BASE/'FROZEN.json').exists()
sel=json.loads((BASE/'dev/selected.json').read_text())
paths=list((ROOT/'scripts/embedbench/research/eg2_ru').glob('*.py'))+list((ROOT/'scripts/embedbench/research/eg2_ru').glob('*.tsv'))+list((BASE/'sets').glob('*.jsonl'))+[BASE/'glossary.json',BASE/'dev/directions.npz',BASE/'PLAN.md']
obj=dict(sel,created=time.strftime('%Y-%m-%d %H:%M:%S'),files={str(p.relative_to(ROOT)):sha(p) for p in paths},primary_pipeline='gloss',pipeline_modes=['plain','gloss','latin','translit'],nohub_sensitivity=True,lexical_modes=['base','stem','gloss'],head_formula='normalize(v-u*(u dot v))',projection_formula='normalize(head(v)-alpha*l*(l dot head(v)))',random_control='orthogonal random unit direction, same absolute alpha, equal rank-one operator Frobenius norm',rig_policy='README read; texts unopened until this freeze; no dictionary extension or tuning afterward',centering='normalize(q - .5*mean(100 hash-selected dev document unit vectors))',models={p:sha(ROOT/p) for p in ['bench/embeddinggemma-2-Q8_0.gguf','bench/Qwen3-Embedding-0.6B-Q8_0.gguf','scripts/embedbench/research/eg2_head_patch/head_mean.npy']})
save(BASE/'FROZEN.json',obj)
log('FROZEN: development-selected projection doses and prespecified pipeline diagnostics hashed before held-out scoring or opening rig look-alike texts. No further tuning authorized by protocol.')
