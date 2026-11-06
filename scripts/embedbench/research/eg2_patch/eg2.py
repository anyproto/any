"""Inference-only MLX implementation of stock gemma-embedding2 GGUF graph.

Loads the exact GGUF tensors, including already-offset RMSNorm gains. Float32
arithmetic avoids BF16 accumulation drift; there is no autograd/training path.
"""
from common import *
import gguf
import mlx.core as mx
from tokenizers import Tokenizer

PREFIX={'doc':'title: none | text: ', 'query':'task: search result | query: '}

class EG2:
    def __init__(self,path=None):
        self.reader=gguf.GGUFReader(str(path or ROOT/'bench/embeddinggemma-2-BF16.gguf'))
        self.weights={}
        for t in self.reader.tensors:
            self.weights[t.name]=mx.array(gguf.dequantize(t.data,t.tensor_type).copy())
        mx.eval(self.weights)
        self.original=dict(self.weights)
        self.tok=Tokenizer.from_file(str(BASE/'hf/tokenizer.json'))
        self.eps=1e-6

    def ids(self,text,role):
        ids=self.tok.encode(PREFIX[role]+text,add_special_tokens=False).ids
        ids=[2]+ids+[1]
        return ids[:2047]+[1] if len(ids)>2048 else ids

    def restore(self): self.weights=dict(self.original)

    def apply(self, edits):
        self.restore()
        for name, value in edits.items(): self.weights[name]=mx.array(value.astype(np.float32))
        mx.eval(self.weights)

    def forward(self, tokens, lengths, capture=None, writer_variants=None):
        w=self.weights; b,n=tokens.shape
        mask=mx.arange(n)[None,:]<lengths[:,None]
        padmask=mx.where(mask[:,None,None,:],0.,-1e9)
        pos=mx.arange(n)
        local=mx.where(mx.abs(pos[:,None]-pos[None,:])<=512,0.,-1e9)[None,None,:,:]+padmask
        h=w['token_embd.weight'][tokens]*np.sqrt(512)
        pe=(h@w['per_layer_model_proj.weight'].T/np.sqrt(512)).reshape(b,n,24,512)
        pe=mx.fast.rms_norm(pe,w['per_layer_proj_norm.weight'],self.eps)
        def rms(x,name):return mx.fast.rms_norm(x,w[name],self.eps)
        def gelu(x):return .5*x*(1+mx.tanh(np.sqrt(2/np.pi)*(x+.044715*x*x*x)))
        if capture is not None:capture('input',-1,h)
        for l in range(24):
            p=f'blk.{l}.'; glob=(l%6==5); hd=512 if glob else 256; kv=1 if glob else 2
            z=rms(h,p+'attn_norm.weight')
            q=(z@w[p+'attn_q.weight'].T).reshape(b,n,4,hd)
            k=(z@w[p+'attn_k.weight'].T).reshape(b,n,kv,hd)
            v=(z@w[p+'attn_v.weight'].T).reshape(b,n,kv,hd)
            q=rms(q,p+'attn_q_norm.weight').transpose(0,2,1,3)
            k=rms(k,p+'attn_k_norm.weight').transpose(0,2,1,3)
            v=mx.fast.rms_norm(v,None,self.eps).transpose(0,2,1,3)
            q=mx.fast.rope(q,hd,traditional=False,base=1e6 if glob else 1e4,scale=1.,offset=0)
            k=mx.fast.rope(k,hd,traditional=False,base=1e6 if glob else 1e4,scale=1.,offset=0)
            a=mx.fast.scaled_dot_product_attention(q,k,v,scale=1.,mask=padmask if glob else local)
            a=a.transpose(0,2,1,3).reshape(b,n,4*hd)
            raw=a@w[p+'attn_output.weight'].T
            out=rms(raw,p+'post_attention_norm.weight')
            if capture is not None:
                capture('attn_in',l,a);capture('attn_raw',l,raw);capture('attn_write',l,out)
            h=h+out
            z=rms(h,p+'ffn_norm.weight')
            a=gelu(z@w[p+'ffn_gate.weight'].T)*(z@w[p+'ffn_up.weight'].T)
            if l==23 and writer_variants is not None:
                assert capture is None
                outputs={}
                for name,down in writer_variants.items():
                    cur=h+rms(a@down.T,p+'post_ffw_norm.weight')
                    gate=gelu(cur@w[p+'inp_gate.weight'].T)*pe[:,:,l,:]
                    cur=(cur+rms(gate@w[p+'proj.weight'].T,p+'post_norm.weight'))*w[p+'layer_output_scale.weight']
                    cur=rms(cur,'output_norm.weight')
                    pooled=(cur*mask[:,:,None]).sum(1)/lengths[:,None]
                    outputs[name]=pooled@w['output.weight'].T
                return outputs
            raw=a@w[p+'ffn_down.weight'].T
            out=rms(raw,p+'post_ffw_norm.weight')
            if capture is not None:
                capture('mlp_in',l,a);capture('mlp_raw',l,raw);capture('mlp_write',l,out)
            h=h+out
            a=gelu(h@w[p+'inp_gate.weight'].T)*pe[:,:,l,:]
            raw=a@w[p+'proj.weight'].T
            out=rms(raw,p+'post_norm.weight')
            if capture is not None:
                capture('ple_in',l,a);capture('ple_raw',l,raw);capture('ple_write',l,out)
            h=(h+out)*w[p+'layer_output_scale.weight']
            if capture is not None:capture('residual',l,h)
        h=rms(h,'output_norm.weight')
        if capture is not None:capture('final_norm',-1,h)
        pooled=(h*mask[:,:,None]).sum(1)/lengths[:,None]
        return pooled@w['output.weight'].T

    def encode(self,rows,progress=True,budget=4096):
        ids=[self.ids(r['text'],r['role']) for r in rows]
        order=sorted(range(len(rows)),key=lambda i:len(ids[i])); out=np.zeros((len(rows),768),np.float32)
        done=0;start=time.time();last=start
        while done<len(order):
            end=done+1
            while end<len(order) and end-done<32 and len(ids[order[end]])*(end-done+1)<=budget:end+=1
            ix=order[done:end];n=max(len(ids[i]) for i in ix)
            ts=mx.array([ids[i]+[0]*(n-len(ids[i])) for i in ix]);lens=mx.array([len(ids[i]) for i in ix])
            y=self.forward(ts,lens);mx.eval(y);out[ix]=np.array(y);done+=len(ix)
            del y,ts,lens;mx.clear_cache()
            if progress and (time.time()-last>25 or done==len(order)):
                print(f'EG2 {done}/{len(rows)}, {done/(time.time()-start):.1f} texts/s',flush=True);last=time.time()
        assert np.isfinite(out).all()
        return out

if __name__=='__main__':
    import argparse
    p=argparse.ArgumentParser();p.add_argument('--gguf');p.add_argument('--records',required=True);p.add_argument('--out',required=True)
    a=p.parse_args();model=EG2(a.gguf);np.save(a.out,model.encode(read(a.records)))
