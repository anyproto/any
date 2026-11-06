"""Post-freeze dev mechanism audit; token-offset boundaries, no edit selection."""
from common import *
from eg2 import EG2,mx,PREFIX
import gguf

def main():
    assert (BASE/'FROZEN.json').exists()
    pairs=read(BASE/'dev/contrast_pairs.jsonl');rows=[dict(r,role='doc') for p in pairs for r in [p['short'],p['body']]]
    rows+=[dict(q,role='query') for q in loadset('dev')[1]]
    rows+=[dict(text='',role='doc'),dict(text='',role='query')]
    model=EG2();dirs=np.load(BASE/'dev/directions.npz');arrays={};bounds=[]
    rr=gguf.GGUFReader(str(BASE/'out/embeddinggemma-2-writer-BF16.gguf'))
    edit={t.name:gguf.dequantize(t.data,t.tensor_type).copy() for t in rr.tensors if t.name=='blk.23.ffn_down.weight'}
    for variant in ['original','writer']:
        model.restore()
        if variant=='writer':model.apply(edit)
        stats={}
        for index,row in enumerate(rows):
            text=PREFIX[row['role']]+row['text'];enc=model.tok.encode(text,add_special_tokens=False);ids=model.ids(row['text'],row['role'])
            end=1+sum(1 for a,b in enc.offsets[:len(ids)-2] if b<=len(PREFIX[row['role']]))
            bounds.append(dict(role=row['role'],chars=len(row['text']),prompt_end=end,tokens=len(ids)))
            cap={};query_cap={}
            def capture(kind,layer,x):
                if kind=='residual':cap[layer]=x
                if variant=='original' and row['role']=='query' and row['text']:query_cap[f'{layer}/{kind}']=x
            y=model.forward(mx.array([ids]),mx.array([len(ids)]),capture);mx.eval(y,cap,query_cap)
            if query_cap:np.savez_compressed(BASE/'dev'/f'query-tokens-{index:03d}.npz',ids=np.array(ids),**{k:np.array(x[0]) for k,x in query_cap.items()})
            for l,x in cap.items():
                x=np.array(x[0]);u=dirs[f'residual/{l}']
                for region,a in [('pooled',x),('bos',x[:1]),('prompt',x[1:end]),('content',x[end:-1] if len(x)>end+1 else x[-1:])]:
                    stats.setdefault(f'{l}/{region}',[]).append(float(a.mean(0)@u))
            del cap,y,query_cap;mx.clear_cache()
        arrays[variant]={k:np.array(x) for k,x in stats.items()}
    n=len(pairs);out={'pairs':n,'token_boundaries':bounds[:len(rows)],'note':'Offset-based complete-prefix-token regions. These replace the initial approximate empty-prefix token-count summaries. Frozen candidates unchanged.','layers':{}}
    for l in range(24):
        out['layers'][str(l)]={}
        for region in ['pooled','bos','prompt','content']:
            k=f'{l}/{region}';a=arrays['original'][k];b=arrays['writer'][k]
            out['layers'][str(l)][region]=dict(original_short_minus_body=delta(a[:2*n:2],a[1:2*n:2]),writer_minus_original_short=delta(b[:2*n:2],a[:2*n:2]),writer_minus_original_body=delta(b[1:2*n:2],a[1:2*n:2]),empty_doc=float(a[-2]),empty_query=float(a[-1]))
    save(BASE/'dev/prompt-audit.json',out)
    # Put writer contributions in common post-layer-scale units for interpretation.
    act=np.load(BASE/'dev/activations.npz');loc=json.loads((BASE/'dev/localization.json').read_text())
    for r in loc['writers']:
        scale=float(np.array(model.original[f'blk.{r["layer"]}.layer_output_scale.weight'])[0]);r['layer_output_scale']=scale;r['relative_after_layer_scale']=r['relative']*scale
    save(BASE/'dev/localization-scaled.json',loc)
    log('Post-freeze dev diagnostic: audited complete doc/query prefix token boundaries and writer effects on BOS/prompt/content states; added layer-scale-corrected writer contribution magnitudes. No candidates or directions changed.')

if __name__=='__main__':main()
