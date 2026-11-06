"""Exact shared-prefix inference for final-MLP variants, with parity gating."""
from common import *
from eg2 import EG2,mx
from encode_eval import records
import argparse,gguf

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--sets',nargs='+',required=True);a=ap.parse_args()
    frozen=json.loads((BASE/'FROZEN.json').read_text());assert (BASE/'validation/results.json').exists()
    model=EG2(ROOT/'bench/embeddinggemma-2-Q8_0.gguf');name='blk.23.ffn_down.weight'
    variants={'original':model.weights[name]}
    for v in ['writer','writer_random']:
        path=ROOT/frozen['variants'][v]['gguf'];r=gguf.GGUFReader(str(path))
        variants[v]=mx.array(next(gguf.dequantize(t.data,t.tensor_type).copy() for t in r.tensors if t.name==name))
        manifest=next(m for m in frozen['manifests'].values() if m['files']['Q8_0']['path']==str(path.relative_to(ROOT)))
        assert {d['name'] for d in manifest['files']['Q8_0']['different_tensors']}=={name}
    mx.eval(variants)
    def encode(rows,progress=True):
        ids=[model.ids(r['text'],r['role']) for r in rows];order=sorted(range(len(rows)),key=lambda i:len(ids[i]));out={v:np.zeros((len(rows),768),np.float32) for v in variants}
        done=0;start=time.time();last=start
        while done<len(order):
            end=done+1
            while end<len(order) and end-done<32 and len(ids[order[end]])*(end-done+1)<=4096:end+=1
            ix=order[done:end];n=max(len(ids[i]) for i in ix)
            y=model.forward(mx.array([ids[i]+[0]*(n-len(ids[i])) for i in ix]),mx.array([len(ids[i]) for i in ix]),writer_variants=variants)
            mx.eval(y)
            for v,x in y.items():out[v][ix]=np.array(x)
            del y;mx.clear_cache();done=end
            if progress and time.time()-last>25:print(f'Joint {done}/{len(rows)}, {done/(time.time()-start):.1f} records/s',flush=True);last=time.time()
        for v in out.values():assert np.isfinite(v).all()
        return out
    val=read(BASE/'validation/records.jsonl');joint=encode(val,False);checks={}
    for v in ['original','writer']:
        full=np.load(BASE/'validation'/f'{v}-Q8_0.npy');c=(norm(joint[v])*norm(full)).sum(1);assert c.min()>=.999999
        checks[v]=dict(min_cosine=float(c.min()),max_abs_error=float(abs(joint[v]-full).max()))
    model.apply({name:np.array(variants['writer_random'])});full=model.encode(val,False);model.restore()
    c=(norm(joint['writer_random'])*norm(full)).sum(1);assert c.min()>=.999999;checks['writer_random']=dict(min_cosine=float(c.min()),max_abs_error=float(abs(joint['writer_random']-full).max()))
    save(BASE/'validation/shared-prefix-parity.json',checks);log('Shared-prefix inference matches independent full forward passes for all three Q8 variants at cosine >=0.999999; commencing frozen held-out bank.')
    rows=records(a.sets);outs={v:BASE/'vectors'/v for v in variants}
    for out in outs.values():
        out.mkdir(parents=True,exist_ok=True)
        if (out/'records.jsonl').exists():assert read(out/'records.jsonl')==rows
        else:write(out/'records.jsonl',rows)
    start=time.time()
    for pos in range(0,len(rows),1000):
        files={v:out/f'chunk-{pos:06d}.npy' for v,out in outs.items()}
        if all(f.exists() for f in files.values()):continue
        result=encode(rows[pos:pos+1000])
        for v,f in files.items():np.save(f,result[v])
        log(f'Joint Q8 checkpoint: {min(pos+1000,len(rows))}/{len(rows)} unique records, three variants, {time.time()-start:.0f}s.')
    for v,out in outs.items():
        files=[out/f'chunk-{pos:06d}.npy' for pos in range(0,len(rows),1000)]
        np.save(out/'raw.npy',np.concatenate([np.load(f) for f in files]))
        for f in files:f.unlink()
        save(out/'run.json',dict(name=v,sets=a.sets,rows=len(rows),joint_seconds=time.time()-start,context=2048,method='exact_shared_prefix_final_MLP'))
    log(f'Frozen EG2 test banks complete: {len(rows)} unique role/text records, all three variants, {time.time()-start:.1f}s.')

if __name__=='__main__':main()
