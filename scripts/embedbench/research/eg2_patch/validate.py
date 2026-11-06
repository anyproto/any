"""Serial native-harness parity and quantization checks for frozen artifacts."""
from common import *
import gguf,subprocess,gc
from eg2 import EG2,mx
from head_sweep import substitute

MATRICES={}
def matrix(path,name):
    if str(path) not in MATRICES:
        r=gguf.GGUFReader(str(path));MATRICES[str(path)]={t.name:gguf.dequantize(t.data,t.tensor_type).copy() for t in r.tensors if t.name in ['output.weight','blk.23.ffn_down.weight']}
    return MATRICES[str(path)][name]
def native(stem,relative,kind,version):
    env=os.environ.copy();env['TMPDIR']=str(BASE/'tmp');env['ANY_BENCH_CTX']='2048';env['ANY_BENCH_CORPUS_OVERRIDE']=str(BASE/'validation/corpus.jsonl')
    env['EMBEDBENCH_BIN']=str(ROOT/('bench/granite/bin' if version=='b11600' else 'bin'))
    env['EMBEDBENCH_LIBDIR']=str(ROOT/('bench/llamacpp-b11600' if version=='b11600' else 'bin/llamacpp'))
    subprocess.run([str(ROOT/'scripts/embedbench/mac/run.sh'),'eg2-patch/validation/'+stem,relative,kind,'metal'],cwd=ROOT,env=env,check=True)
def vectors(stem):
    return np.concatenate([np.fromfile(BASE/'validation'/f'{stem}.{role}.f32',dtype='<f4',offset=8).reshape(-1,768 if 'qwen' not in stem else 1024) for role in ['docs','queries']])
def compare(x,y):
    x=norm(x);y=norm(y);c=(x*y).sum(1)
    return dict(min_cosine=float(c.min()),mean_cosine=float(c.mean()),cosines=c.tolist(),max_abs_error=float(abs(x-y).max()))

def main():
    assert (BASE/'FROZEN.json').exists()
    rows=read(BASE/'validation/records.jsonl');res={};vv={}
    for dtype in ['BF16','Q8_0']:
        model=EG2(ROOT/'bench'/f'embeddinggemma-2-{dtype}.gguf')
        for name,stem in [('original',None),('head','embeddinggemma-2-patched'),('writer','embeddinggemma-2-writer')]:
            model.restore()
            if stem:
                manifest=json.loads((BASE/'out'/f'{stem}-manifest.json').read_text())
                path=BASE/'out'/f'{stem}-{dtype}.gguf'
                model.apply({r['tensor']:matrix(path,r['tensor']) for r in manifest['tensors']})
            v=model.encode(rows);vv[name+'/'+dtype]=v;np.save(BASE/'validation'/f'{name}-{dtype}.npy',v)
        del model;gc.collect();mx.clear_cache()
    for name in ['original','head','writer']:res[name+'_q8_vs_bf16']=compare(vv[name+'/Q8_0'],vv[name+'/BF16'])
    original_path=ROOT/'bench/embeddinggemma-2-Q8_0.gguf';head_path=BASE/'out/embeddinggemma-2-patched-Q8_0.gguf'
    cached=substitute(vv['original/Q8_0'],matrix(original_path,'output.weight'),matrix(head_path,'output.weight'))
    res['cached_head_vs_full_forward']=compare(cached,vv['head/Q8_0']);assert res['cached_head_vs_full_forward']['min_cosine']>=.999999
    # Q8_0 reference quantization must reproduce ggml-org on both edited sites.
    b=gguf.GGUFReader(str(ROOT/'bench/embeddinggemma-2-BF16.gguf'));q=gguf.GGUFReader(str(original_path));bm={t.name:t for t in b.tensors};qm={t.name:t for t in q.tensors}
    res['q8_reference_quantizer']={}
    for name in ['output.weight','blk.23.ffn_down.weight']:
        a=gguf.quantize(gguf.dequantize(bm[name].data,bm[name].tensor_type),qm[name].tensor_type)
        res['q8_reference_quantizer'][name]=dict(bytes=int(a.nbytes),different_bytes=int(np.count_nonzero(a!=qm[name].data)))
    del b,q,bm,qm;gc.collect()
    configs=[('original','Q8_0','b11600'),('head','BF16','b11600'),('head','Q8_0','b11600'),('head','BF16','b11454'),('head','Q8_0','b11454'),('writer','Q8_0','b11600')]
    for name,dtype,version in configs:
        stem=f'{name}-{dtype}-{version}'
        relative=f'embeddinggemma-2-{dtype}.gguf' if name=='original' else f'eg2-patch/out/embeddinggemma-2-{"patched" if name=="head" else "writer"}-{dtype}.gguf'
        native(stem,relative,'eg2',version)
        res[stem]=compare(vv[name+'/'+dtype],vectors(stem));assert res[stem]['min_cosine']>=.9999,(stem,res[stem])
        save(BASE/'validation/results.json',res);log(f'Stock {stem} parity passed: min cosine {res[stem]["min_cosine"]:.8f}.')
    from qwen import Qwen
    model=Qwen();qv=model.encode(rows);np.save(BASE/'validation/qwen-Q8_0.npy',qv);del model;gc.collect();mx.clear_cache()
    native('qwen-Q8_0-b11600','Qwen3-Embedding-0.6B-Q8_0.gguf','qwen3','b11600')
    res['qwen-Q8_0-b11600']=compare(qv,vectors('qwen-Q8_0-b11600'));assert res['qwen-Q8_0-b11600']['min_cosine']>=.9999
    save(BASE/'validation/results.json',res);log('Completed all native and Python parity checks, Q8/BF16 drift checks, head caching equivalence, and Qwen Q8 baseline verification.')

if __name__=='__main__':main()
