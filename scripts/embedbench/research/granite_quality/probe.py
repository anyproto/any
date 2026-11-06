"""Layer/BOS/position causal probes, batched reference checks, truncation audit."""
from common import *
import torch,transformers
from transformers import AutoTokenizer,AutoModel
torch.set_num_threads(2)
tok=AutoTokenizer.from_pretrained(ROOT/'bench/granite/hf',local_files_only=True)
model=AutoModel.from_pretrained(ROOT/'bench/granite/hf',local_files_only=True,torch_dtype=torch.float32,attn_implementation='sdpa').to('mps').eval();model.config.reference_compile=False
docs,_,_=loadset('wiki');pairs=[]
for lang in ['en','de','ru','ja','zh']:
    found=[]
    for d,n in zip(docs,docs[1:]):
        if d['lang']==lang and d['heading_only'] and len(d['text'])<80 and not n['heading_only'] and n['article']==d['article'] and len(n['text'])>200:
            found.append((d,n))
    rng=np.random.default_rng(391);rng.shuffle(found);pairs+=found[:10]
rows=[x for p in pairs for x in p]+[dict(id='empty',text='',lang='none')]
write(BASE/'sets/probes/docs.jsonl',rows)
out={};traces=[];rawnorm=[];variants={k:[] for k in ['base','bos_zero','bos_eos','bos_double','position_shift']}
with torch.inference_mode():
    for d in rows:
        enc=tok(d['text'],truncation=True,max_length=2048,return_tensors='pt').to('mps')
        r=model(**enc,output_hidden_states=True)
        h=torch.stack([x[0,0] for x in r.hidden_states]+[r.last_hidden_state[0,0]])
        traces.append(h.float().cpu().numpy());rawnorm.append(torch.linalg.vector_norm(h,dim=1).cpu().numpy())
        variants['base'].append(r.last_hidden_state[0,0].cpu().numpy())
        for variant in ['bos_zero','bos_eos','bos_double','position_shift']:
            if variant=='position_shift':
                v=model(**enc,position_ids=torch.arange(enc['input_ids'].shape[1],device='mps')[None,:]+37)
            else:
                emb=model.embeddings.tok_embeddings(enc['input_ids']).clone()
                if variant=='bos_zero':emb[:,0]=0
                elif variant=='bos_eos':emb[:,0]=model.embeddings.tok_embeddings.weight[1]
                else:emb[:,0]*=2
                v=model(inputs_embeds=emb,attention_mask=enc['attention_mask'])
            variants[variant].append(v.last_hidden_state[0,0].cpu().numpy())
    traces=np.array(traces);rawnorm=np.array(rawnorm);variants={k:np.array(v) for k,v in variants.items()}
    np.savez(BASE/'debug/probe_vectors.npz',traces=traces,norms=rawnorm,**variants)
    t=norm(traces);n=len(pairs)
    out['n_pairs']=n;out['layer_index']='0=input embedding; 1..22=after encoder blocks before final_norm; 23=after final_norm'
    out['layers']=[]
    for l in range(t.shape[1]):
        short=t[:-1:2,l];long=t[1:-1:2,l];bos=t[-1,l]
        out['layers'].append(dict(layer=l,short_empty_cosine=float((short@bos).mean()),long_empty_cosine=float((long@bos).mean()),paired_short_minus_long=delta(short@bos,long@bos),short_norm=float(rawnorm[:-1:2,l].mean()),long_norm=float(rawnorm[1:-1:2,l].mean()),norm_short_minus_long=delta(rawnorm[:-1:2,l],rawnorm[1:-1:2,l])))
    for k,v in variants.items():
        cos=(norm(v)*norm(variants['base'])).sum(1)
        out[k]=dict(mean_cosine_to_original=float(cos[:-1].mean()),min_cosine=float(cos.min()),short_minus_long_direction_change=delta(1-cos[:-1:2],1-cos[1:-1:2]))
    b=bank('granite');base=norm(vectors(b,rows[:-1]));cos=(base*norm(variants['base'][:-1])).sum(1)
    out['batch_vs_single']={'min_cosine':float(cos.min()),'mean_cosine':float(cos.mean())}
    scale=model.final_norm.weight.cpu().numpy();out['final_norm_scale']={'min':float(scale.min()),'max':float(scale.max()),'max_abs_dims':np.argsort(-abs(scale))[:16].tolist()}
    # Audit token sequence handling; a targeted production-tail pass updates only truncated records.
    records=read(BASE/'vectors/granite/records.jsonl');rawids=[tok.encode(r['text']) for r in records]
    trunc=[i for i,x in enumerate(rawids) if len(x)>2048]
    out['tokenizer']={'empty':tok.encode(''),'hello':tok.encode('hello'),'ru':tok.encode('Привет'),'truncated_records':len(trunc),'total_records':len(records)}
    arrays={k:b[1][k].copy() for k in b[1].files};cosines=[]
    capture={};handles=[]
    for l in [10,16,19,21]:
        def hook(module,args,res,l=l):capture[l]=res[0][:,0].clone()
        handles.append(model.layers[l-1].register_forward_hook(hook))
    for i in trunc:
        ids=torch.tensor([rawids[i][:2047]+[1]],device='mps');h=model(input_ids=ids).last_hidden_state
        v=h[:,0].cpu().numpy()[0];cosines.append(float(norm(v)@norm(arrays['cls'][i])))
        arrays['cls'][i]=v;arrays['mean'][i]=h.mean(1).cpu().numpy()[0];arrays['mean_content'][i]=h[:,1:].mean(1).cpu().numpy()[0]
        for l in [10,16,19,21]:arrays['layer'+str(l)][i]=model.final_norm(capture[l]).cpu().numpy()[0]
        capture.clear()
    np.savez(BASE/'vectors/granite/vectors.npz',**arrays)
    out['truncation_tail_cosines']=cosines
    out['truncated_keys']=[records[i]['key'] for i in trunc]
(BASE/'debug/probes.json').write_text(json.dumps(out,indent=2))
log(f'Layer/BOS/position probes complete: {len(pairs)} matched heading/body pairs; {len(trunc)} over-limit records re-encoded with production EOS tail. Original-vs-production cosine saved.')
