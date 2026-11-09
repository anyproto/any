from util import *
from pipeline import rewrite,transliterate
import argparse,re
PROMPTS={'bare':'','ru':'задача: поиск подходящего текста | запрос: ','paraphrase':'task: find a passage with the same meaning | query: '}
def make_bank(split):
 bank={}
 def add(t,role,prompt='prod'):
  k=key(t,role if prompt=='prod' else role+'/'+prompt);bank[k]=dict(key=k,text=t,role=role,prompt=prompt)
 background=read(BASE/'sets/public-background.jsonl')
 for r in background:
  for mode in ['original','plain','gloss','latin','translit']:add(rewrite(r['text'],mode),'doc')
 for source in ['synthetic','public']:
  for r in read(BASE/'sets'/f'{source}.jsonl'):
   if r['split']!=split:continue
   for mode in ['original','plain','gloss','latin','translit']:add(rewrite(r['text'],mode),'doc')
   for kind in ['kw','para','cross']:
    add(r[kind],'query')
    if split=='dev':
     for prompt in PROMPTS:add(r[kind],'query',prompt)
   if source=='synthetic':
    add(r['english'],'doc');add(r['base_text'],'doc')
    for t in [r['base_text'],r['english'],rewrite(r['base_text'],'plain')]:add(t,'doc')
    # Topical short non-answer, identical across corpus renderings.
    add('Обсуждение: '+r['kw']+'. Подробности будут позже.','doc')
    word=re.search(r['pattern'],r['text'],re.I)[0]
    for t in [word,r['en'],r['ru'],transliterate(word)]:
     for role in ['doc','query']:add(t,role)
 # Add transformed hub texts after the existing deterministic bank prefix.
 for r in read(BASE/'sets/synthetic.jsonl'):
  if r['split']==split:
   for mode in ['plain','gloss','latin','translit']:add(rewrite('Обсуждение: '+r['kw']+'. Подробности будут позже.',mode),'doc')
 add('debug build, client state, pending check','doc')
 return list(bank.values())
def main():
 p=argparse.ArgumentParser();p.add_argument('split',choices=['dev','test','rig']);p.add_argument('model',choices=['eg2','qwen']);a=p.parse_args()
 if a.split!='dev':assert (BASE/'FROZEN.json').exists()
 if a.split=='rig':
  rows=read(BASE/'sets/rig-bank.jsonl')
 else:rows=make_bank(a.split)
 out=BASE/'vectors'/f'{a.split}-{a.model}';out.mkdir(exist_ok=True)
 if (out/'records.jsonl').exists():
  old=read(out/'records.jsonl');assert rows[:len(old)]==old
  write(out/'records.jsonl',rows)
 else:write(out/'records.jsonl',rows)
 if a.model=='eg2':
  from eg2 import EG2
  class PromptEG2(EG2):
   active='prod'
   def ids(self,text,role):
    if self.active=='prod':return super().ids(text,role)
    ids=[2]+self.tok.encode(PROMPTS[self.active]+text,add_special_tokens=False).ids+[1]
    return ids[:2047]+[1] if len(ids)>2048 else ids
  m=PromptEG2(ROOT/'bench/embeddinggemma-2-Q8_0.gguf')
 else:
  from qwen import Qwen
  m=Qwen()
 # Qwen only production records. Keep unused prompt rows zero, never retrieve them.
 result=np.zeros((len(rows),768 if a.model=='eg2' else 1024),np.float32);start=time.time()
 for prompt in ['prod']+list(PROMPTS):
  ix=[i for i,r in enumerate(rows) if r['prompt']==prompt]
  if a.model=='qwen' and prompt!='prod':continue
  if a.model=='eg2':m.active=prompt
  for st in range(0,len(ix),100):
   jj=ix[st:st+100];f=out/f'{prompt}-{st:05}.npy'
   if f.exists() and len(np.load(f,mmap_mode='r'))==len(jj):v=np.load(f)
   else:v=m.encode([rows[i] for i in jj],budget=2048);np.save(f,v)
   result[jj]=v
   print(a.split,a.model,prompt,st+len(jj),len(ix),round(time.time()-start),flush=True)
 np.save(out/'raw.npy',result);save(out/'run.json',dict(seconds=time.time()-start,rows=len(rows),model=a.model,split=a.split,context=2048))
 log(f'Encoded {a.split}/{a.model}: {len(rows)} bank rows in {time.time()-start:.1f}s; production Q8, role prefixes, 2048 context. No retrieval scored here.')
if __name__=='__main__':main()
