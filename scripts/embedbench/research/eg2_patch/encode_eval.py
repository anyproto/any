"""Encode a frozen test record bank, serially, with resume-safe chunk files.

This does not score retrieval. Candidate specifications must already be frozen.
"""
from common import *
import argparse

def records(sets):
    bank={}
    for name in sets:
        ds,qs,_=loadset(name)
        for role,rs in [('doc',ds),('query',qs)]:
            for r in rs:
                k=key(r['text'],role);bank[k]=dict(key=k,text=r['text'],role=role)
    return list(bank.values())

def main():
    p=argparse.ArgumentParser();p.add_argument('name');p.add_argument('--gguf');p.add_argument('--qwen',action='store_true');p.add_argument('--sets',nargs='+',required=True);p.add_argument('--validation',action='store_true');a=p.parse_args()
    assert (BASE/'FROZEN.json').exists(),'Freeze final specs before encoding held-out data'
    out=BASE/'vectors'/a.name;out.mkdir(parents=True,exist_ok=True)
    rows=read(BASE/'validation/records.jsonl') if a.validation else records(a.sets)
    if (out/'records.jsonl').exists():assert read(out/'records.jsonl')==rows,'Record order changed'
    else:write(out/'records.jsonl',rows)
    if a.qwen:
        from qwen import Qwen
        model=Qwen()
    else:
        from eg2 import EG2
        model=EG2(a.gguf)
    start=time.time();files=[]
    for pos in range(0,len(rows),1000):
        f=out/f'chunk-{pos:06d}.npy';files.append(f)
        if f.exists():continue
        v=model.encode(rows[pos:pos+1000]);np.save(f,v)
        log(f'Encoding checkpoint {a.name}: {pos+len(v)}/{len(rows)} records, elapsed {time.time()-start:.0f}s.')
    allv=np.concatenate([np.load(f) for f in files]);np.save(out/'raw.npy',allv)
    for f in files:f.unlink()
    save(out/'run.json',dict(name=a.name,sets=a.sets,rows=len(rows),seconds=time.time()-start,gguf=a.gguf,qwen=a.qwen,context=2048))
    log(f'Encoded frozen test bank {a.name}: {len(rows)} unique role/text records in {time.time()-start:.1f}s. No retrieval feedback used for tuning.')

if __name__=='__main__':main()
