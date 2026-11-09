"""Serial final confirmation; no tuning or network/account access."""
from util2 import *
from data2 import SETS
import subprocess
def main():
 fr=json.loads((BASE/'FROZEN.json').read_text());verify_seal()
 for p,h in fr['files'].items():assert sha(ROOT/p)==h,p
 rigdir=BASE/'from-rig';manifest=[];new=[]
 if rigdir.exists():
  for p in sorted(rigdir.glob('*.jsonl')):
   rr=read(p);manifest.append(dict(path=str(p.relative_to(ROOT)),sha256=sha(p),records=len(rr)))
   for r in rr:
    assert all(k in r for k in ['id','text','kw_query','para_query']),f'Unsupported approved rig record schema: {p}'
    new.append(r)
 save(BASE/'results/new-rig-inventory.json',dict(at=datetime.datetime.now(datetime.timezone.utc).isoformat(),files=manifest,records=len(new)))
 if new:
  assert len({r['id'] for r in new})==len(new)
  ds=read(OLD/'sets/public-background.jsonl')+new;qs=[]
  for r in new:
   for k in ['kw','para']:qs.append(dict(id=r['id']+':'+k,target=r['id'],group=r['id'],text=r[k+'_query'],kind=k))
  write(BASE/'sets/new-rig-docs.jsonl',ds);write(BASE/'sets/new-rig-queries.jsonl',qs)
 for model in ['eg2','qwen']:
  with (BASE/'results'/f'encode-{model}.log').open('a') as f:subprocess.run([sys.executable,str(CODE/'run2.py'),'encode','heldout',model],stdout=f,stderr=subprocess.STDOUT,check=True)
 for name in ['sealed','sealed-ru','round1','rig']+(['new-rig'] if new else [])+SETS:
  if (BASE/'results'/f'{name}.json').exists():continue
  with (BASE/'results'/f'score-{name}.log').open('a') as f:subprocess.run([sys.executable,str(CODE/'run2.py'),'score',name],stdout=f,stderr=subprocess.STDOUT,check=True)
 log('All frozen confirmation arms scored serially on sealed, round-1, available rig and six public sets; no held-out-dependent tuning.')
if __name__=='__main__':main()
