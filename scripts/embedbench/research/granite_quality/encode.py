"""Batched local-only inference. Run one model at a time. No model files changed."""
from common import *
import argparse,time
ap=argparse.ArgumentParser();ap.add_argument('model',choices=['granite','qwen']);ap.add_argument('--sets',nargs='+',default=['wiki','wiki-fold','wiki-mix','ru','flores-dev','flores-devtest','fiqa10k']);ap.add_argument('--limit',type=int,default=0);a=ap.parse_args()
records={}
for name in a.sets:
    docs,qs,_=loadset(name)
    for role,rows in [('doc',docs),('query',qs)]:
        for r in rows:
            k=key(r['text'],role);records[k]=dict(key=k,text=r['text'],role=role)
rows=list(records.values())
if a.limit:rows=rows[:a.limit]
out=BASE/'vectors'/a.model;out.mkdir(parents=True,exist_ok=True)
write(out/'records.jsonl',rows)
start=time.time()
if a.model=='granite':
    import torch,transformers
    from transformers import AutoModel,AutoTokenizer
    torch.set_num_threads(2)
    tok=AutoTokenizer.from_pretrained(ROOT/'bench/granite/hf',local_files_only=True)
    model=AutoModel.from_pretrained(ROOT/'bench/granite/hf',local_files_only=True,torch_dtype=torch.float32,attn_implementation='sdpa').to('mps').eval()
    model.config.reference_compile=False
    ids=[tok.encode(r['text']) for r in rows]
    ids=[x[:2047]+[1] if len(x)>2048 else x for x in ids]
    variants={v:np.zeros((len(rows),768),dtype=np.float32) for v in ['cls','mean','mean_content','layer10','layer16','layer19','layer21']}
    capture={}
    handles=[]
    for layer in [10,16,19,21]:
        def hook(module,args,output,l=layer):capture[l]=output[0][:,0].clone()
        handles.append(model.layers[layer-1].register_forward_hook(hook))
    order=sorted(range(len(rows)),key=lambda i:len(ids[i]))
    done=0
    with torch.inference_mode():
        while done<len(order):
            length=len(ids[order[done]])
            bs=min(16,max(1,2048//max(32,length)))
            ix=order[done:done+bs]
            enc=tok.pad({'input_ids':[ids[i] for i in ix]},padding=True,return_tensors='pt').to('mps')
            h=model(**enc).last_hidden_state
            mask=enc['attention_mask'].unsqueeze(-1)
            variants['cls'][ix]=h[:,0].float().cpu().numpy()
            variants['mean'][ix]=((h*mask).sum(1)/mask.sum(1)).float().cpu().numpy()
            mask[:,0]=0
            variants['mean_content'][ix]=((h*mask).sum(1)/mask.sum(1).clamp_min(1)).float().cpu().numpy()
            for i in ix:
                if len(ids[i])==1:variants['mean_content'][i]=variants['cls'][i]
            for l,v in capture.items():variants['layer'+str(l)][ix]=model.final_norm(v).float().cpu().numpy()
            done+=len(ix)
            if done%128<len(ix) or done==len(order):print(f'granite {done}/{len(rows)}, {done/(time.time()-start):.1f} texts/s, len={length}',flush=True)
            del h,enc,mask;capture.clear()
            torch.mps.empty_cache()
    extra={'torch':torch.__version__,'transformers':transformers.__version__,'dtype':'float32','device':'mps'}
else:
    import mlx.core as mx
    from mlx_lm.models import qwen3
    from tokenizers import Tokenizer
    path=ROOT/'bench/mlx/qwen3-emb-0.6b'
    cfg=json.loads((path/'config.json').read_text());model=qwen3.Model(qwen3.ModelArgs.from_dict(cfg))
    w=mx.load(str(path/'model.safetensors'))
    model.load_weights([('model.'+k,v.astype(mx.bfloat16)) for k,v in w.items()],strict=False);del w
    model.eval();mx.eval(model.parameters());tok=Tokenizer.from_file(str(path/'tokenizer.json'))
    prefix='Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:'
    ids=[]
    for r in rows:
        x=tok.encode((prefix if r['role']=='query' else '')+r['text']).ids
        if not x or x[-1]!=151643:x.append(151643)
        ids.append(x[:2047]+[151643] if len(x)>2048 else x)
    variants={'cls':np.zeros((len(rows),1024),dtype=np.float32)}
    order=sorted(range(len(rows)),key=lambda i:len(ids[i]));done=0
    while done<len(order):
        length=len(ids[order[done]]);bs=min(16,max(1,3072//max(length,32)))
        ix=order[done:done+bs];n=max(len(ids[i]) for i in ix);n=((n+31)//32)*32
        # Right padding cannot affect preceding valid states in a causal decoder.
        tokens=mx.array([ids[i]+[0]*(n-len(ids[i])) for i in ix])
        h=model.model(tokens)[mx.arange(len(ix)),mx.array([len(ids[i])-1 for i in ix])].astype(mx.float32)
        mx.eval(h);variants['cls'][ix]=np.array(h);del h,tokens;mx.clear_cache()
        done+=len(ix)
        if done%128<len(ix) or done==len(order):print(f'qwen {done}/{len(rows)}, {done/(time.time()-start):.1f} texts/s, len={length}',flush=True)
    checks=[]
    rng=np.random.default_rng(391)
    for i in rng.choice([i for i,x in enumerate(ids) if len(x)<300],size=12,replace=False):
        h=model.model(mx.array([ids[i]]))[0,-1].astype(mx.float32);mx.eval(h)
        checks.append(float(norm(np.array(h))@norm(variants['cls'][i])));mx.clear_cache()
    extra={'mlx':mx.__version__,'dtype':'bfloat16','device':'metal','batch_single_cosines':checks}
np.savez(out/'vectors.npz',**variants)
np.save(out/'token_lengths.npy',np.array([len(i) for i in ids]))
(out/'run.json').write_text(json.dumps(dict(count=len(rows),seconds=time.time()-start,sets=a.sets,max_tokens=2048,**extra),indent=2))
log(f'Encoded {len(rows)} unique role/text pairs using {a.model}; {time.time()-start:.1f}s; raw vectors retained temporarily for geometry, pooling, and calibration.')
