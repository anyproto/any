"""Serial native end-to-end verification, public synthetic corpus, pipe IPC only."""
from util import *
from pipeline import rewrite
import subprocess,re,gc

def run():
 rr=[r for r in read(BASE/'sets/synthetic.jsonl') if r['split']=='dev'][:6]
 ds=[dict(text=rewrite(r['text'],mode),role='doc') for r in rr for mode in ['original','gloss']]
 write(BASE/'validation/corpus.jsonl',ds)
 src=(ROOT/'internal/indexer/embed_bench_test.go').read_text();part=src.split('queries := []string{',1)[1].split('}',1)[0]
 qs=[dict(text=s,role='query') for s in re.findall(r'"([^"\n]*)"',part)];rows=ds+qs
 results={};pyvec={}
 for name,file,kind in [('stock','embeddinggemma-2-Q8_0.gguf','eg2'),('head','eg2-patch/out/embeddinggemma-2-patched-Q8_0.gguf','eg2'),('qwen','Qwen3-Embedding-0.6B-Q8_0.gguf','qwen3')]:
  env=os.environ.copy();env.update(ANY_BENCH_CTX='2048',ANY_BENCH_CORPUS_OVERRIDE=str(BASE/'validation/corpus.jsonl'),EMBEDBENCH_BIN=str(ROOT/'bench/granite/bin'),EMBEDBENCH_LIBDIR=str(ROOT/'bench/llamacpp-b11600'))
  subprocess.run([str(ROOT/'scripts/embedbench/mac/run.sh'),'eg2-ru/validation/'+name,file,kind,'metal'],env=env,cwd=ROOT,check=True)
  if name=='qwen':
   from qwen import Qwen
   m=Qwen()
  else:
   from eg2 import EG2
   m=EG2(ROOT/'bench'/file)
  v=m.encode(rows,budget=2048);pyvec[name]=v;del m;gc.collect()
  import mlx.core as mx
  mx.clear_cache()
  native=np.concatenate([np.fromfile(BASE/'validation'/f'{name}.{role}.f32',dtype='<f4',offset=8).reshape(-1,v.shape[1]) for role in ['docs','queries']])
  cos=(norm(native)*norm(v)).sum(1);results[name]=dict(min_cosine=float(cos.min()),mean_cosine=float(cos.mean()),rows=len(v));assert cos.min()>=.9999,results[name]
  save(BASE/'validation/parity.json',results)
 from evaluate import head
 cos=(head(norm(pyvec['stock']))*norm(pyvec['head'])).sum(1)
 results['exact_projection_vs_quantized_head']=dict(min_cosine=float(cos.min()),mean_cosine=float(cos.mean()))
 save(BASE/'validation/parity.json',results);log('Production b11600 native parity passed for stock EG2, patched Q8 head, Qwen, including glossary-expanded public synthetic docs; pipes only, no server or local port.')
if __name__=='__main__':run()
