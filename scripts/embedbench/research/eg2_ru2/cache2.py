from util2 import *
from data2 import public_index
class Bank:
 def __init__(self,model,scope):
  self.model=model;self.scope=scope;self.lookup={};self.arrays=[]
  for split in ['dev']+(['test','rig'] if scope!='dev' else []):
   p=OLD/'vectors'/f'{split}-{model}'
   rr=read(p/'records.jsonl');v=np.load(p/'raw.npy',mmap_mode='r');slot=len(self.arrays);self.arrays.append(v)
   for i,r in enumerate(rr):
    if r.get('prompt','prod')=='prod':self.lookup[key(r['text'],r['role'])]=(slot,i)
  idx,v,_=public_index(model);slot=len(self.arrays);self.arrays.append(v)
  self.lookup.update({k:(slot,i) for k,i in idx.items()})
  self.out=BASE/'vectors'/f'{scope}-{model}';self.out.mkdir(exist_ok=True)
  self.local=read(self.out/'records.jsonl') if (self.out/'records.jsonl').exists() else []
  self.values=list(np.load(self.out/'raw.npy')) if self.local else []
  self.localidx={r['key']:i for i,r in enumerate(self.local)}
 def get(self,r,variant='base'):
  k=key(r['text'],r['role']);lk=variant+':'+k
  if lk in self.localidx:return self.values[self.localidx[lk]]
  if variant=='base' and k in self.lookup:
   a,i=self.lookup[k];return np.asarray(self.arrays[a][i])
  return None
 def put(self,r,v,variant):
  k=variant+':'+key(r['text'],r['role']);assert k not in self.localidx
  self.localidx[k]=len(self.local);self.local.append(dict(r,key=k,variant=variant));self.values.append(v)
 def flush(self):
  np.save(self.out/'raw.npy',np.asarray(self.values,dtype=np.float32));write(self.out/'records.jsonl',self.local)
 def ensure(self,model,rows,variant='base',edited=None):
  unique={key(r['text'],r['role']):r for r in rows};todo=[];edited=set(edited) if edited is not None else None
  for r in unique.values():
   if self.get(r,variant) is not None:continue
   if variant!='base' and not any(t in edited for t in model.ids(r['text'],r['role'])):
    v=self.get(r,'base');assert v is not None;self.put(r,v,variant)
   else:todo.append(r)
  for start in range(0,len(todo),100):
   chunk=todo[start:start+100];v=model.encode(chunk,budget=2048)
   for r,x in zip(chunk,v):self.put(r,x,variant)
   self.flush();print('encoded',self.scope,self.model,variant,start+len(chunk),len(todo),flush=True)
  self.flush()
  return len(todo)
 def matrix(self,rows,role,variant='base'):
  vals=[self.get(dict(text=r['text'],role=role),variant) for r in rows]
  assert all(v is not None for v in vals),(self.scope,self.model,variant)
  return norm(np.asarray(vals,dtype=np.float32))
