"""Validate complete five-bucket corpus coverage and retained-reference overlap."""
import argparse
import hashlib
import json
from pathlib import Path
import numpy as np
from buckets import LENGTHS,MAX_LENGTH

def normalized(a):return a/np.linalg.norm(a,axis=-1,keepdims=True)
def compare(a,b):
    raw_max=float(np.abs(a-b).max())
    a,b=normalized(a.astype(np.float64)),normalized(b.astype(np.float64))
    c=np.sum(a*b,axis=1)
    return dict(count=len(c),meanCosine=float(c.mean()),minCosine=float(c.min()),p01Cosine=float(np.quantile(c,.01)),maxAbs=float(np.abs(a-b).max()),rawMaxAbs=raw_max)
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
p=argparse.ArgumentParser();p.add_argument('--reference',type=Path,action='append',default=[]);p.add_argument('--bf16',type=Path);a=p.parse_args()
own=json.loads(Path('evidence/cold-vectors.json').read_text());ix=own['indices'];v=np.array(own['vectors']);rows=json.loads(Path('tokens.json').read_text())
assert own['sequenceLengths']==list(LENGTHS) and own['maxSequenceLength']==MAX_LENGTH
assert ix==[i for i in range(1500) if len(rows[i*2]['tokens'])<=MAX_LENGTH]
assert len(ix)==1499 and len(set(ix))==1499 and v.shape==(1499,768) and np.isfinite(v).all()
norms=np.linalg.norm(v,axis=1);assert np.max(np.abs(norms-1))<=.001
excluded=[dict(documentIndex=i,tokens=len(rows[i*2]['tokens'])) for i in range(1500) if i not in ix]
assert excluded==[dict(documentIndex=178,tokens=2212)]
long=[i for i in ix if len(rows[i*2]['tokens'])>1024];assert len(long)==8
out=dict(documents=1499,finite=True,zeroVectors=0,normRange=[float(norms.min()),float(norms.max())],excludedOverMaxLength=excluded,new2048DocumentIndices=long,referenceComparisons=[])
for path in a.reference:
    ref=json.loads(path.read_text());mapping=dict(zip(ref['indices'],ref['vectors']));positions=[j for j,i in enumerate(ix) if i in mapping]
    stat=compare(v[positions],np.array([mapping[ix[j]] for j in positions]));assert stat['minCosine']>.999
    out['referenceComparisons'].append(dict(path=str(path),sha256=sha(path),scope='Retained native normalized raw embeddings on overlapping documents only; not a new stock BF16 reference.',**stat))
if a.bf16:
    n,d=np.fromfile(a.bf16,dtype='<u4',count=2);b=np.fromfile(a.bf16,dtype='<f4',offset=8).reshape(n,d)[ix]
    out['optionalRetainedBF16']=dict(path=str(a.bf16),sha256=sha(a.bf16),**compare(v,b))
else:out['bf16ReferenceLimitation']='Historical BF16 corpus file is unavailable; no new BF16 corpus comparison is claimed. Fresh stock-HF FP32 parity is separately recorded for16 native fixtures, including1151/1630/2048-token text.'
Path('evidence/corpus-parity.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps(out,indent=2))
