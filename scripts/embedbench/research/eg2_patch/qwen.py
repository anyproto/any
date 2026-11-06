"""Qwen baseline from production Q8_0; float16 math gated by stock parity."""
from common import *
import gguf,mlx.core as mx
from mlx_lm.models import qwen3
from tokenizers import Tokenizer

class Qwen:
    def __init__(self,dtype='float16'):
        path=ROOT/'bench/mlx/qwen3-emb-0.6b'
        cfg=json.loads((path/'config.json').read_text());self.model=qwen3.Model(qwen3.ModelArgs.from_dict(cfg))
        r=gguf.GGUFReader(str(ROOT/'bench/Qwen3-Embedding-0.6B-Q8_0.gguf'))
        names={'token_embd':'model.embed_tokens','output_norm':'model.norm'}
        layers={'attn_q':'self_attn.q_proj','attn_k':'self_attn.k_proj','attn_v':'self_attn.v_proj','attn_output':'self_attn.o_proj',
                'attn_q_norm':'self_attn.q_norm','attn_k_norm':'self_attn.k_norm','attn_norm':'input_layernorm',
                'ffn_norm':'post_attention_layernorm','ffn_gate':'mlp.gate_proj','ffn_up':'mlp.up_proj','ffn_down':'mlp.down_proj'}
        weights=[]
        for t in r.tensors:
            part=t.name.split('.')
            name=(f'model.layers.{part[1]}.'+layers[part[2]]) if part[0]=='blk' else names[part[0]]
            weights.append((name+'.weight',mx.array(gguf.dequantize(t.data,t.tensor_type).copy()).astype(getattr(mx,dtype))))
        self.model.load_weights(weights,strict=False);self.model.eval();mx.eval(self.model.parameters());self.tok=Tokenizer.from_file(str(path/'tokenizer.json'))

    def ids(self,text,role):
        prefix='Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:' if role=='query' else ''
        x=self.tok.encode(prefix+text).ids
        if not x or x[-1]!=151643:x.append(151643)
        return x[:2047]+[151643] if len(x)>2048 else x

    def encode(self,rows,progress=True,budget=4096):
        ids=[self.ids(r['text'],r['role']) for r in rows];order=sorted(range(len(rows)),key=lambda i:len(ids[i]));out=np.zeros((len(rows),1024),np.float32)
        start=time.time();last=start;done=0
        while done<len(order):
            end=done+1
            while end<len(order) and end-done<32 and len(ids[order[end]])*(end-done+1)<=budget:end+=1
            ix=order[done:end];n=max(len(ids[i]) for i in ix)
            ts=mx.array([ids[i]+[0]*(n-len(ids[i])) for i in ix])
            h=self.model.model(ts)[mx.arange(len(ix)),mx.array([len(ids[i])-1 for i in ix])].astype(mx.float32)
            mx.eval(h);out[ix]=np.array(h);del h,ts;mx.clear_cache();done+=len(ix)
            if progress and (time.time()-last>25 or done==len(order)):
                print(f'Qwen {done}/{len(rows)}, {done/(time.time()-start):.1f} texts/s',flush=True);last=time.time()
        assert np.isfinite(out).all();return out
