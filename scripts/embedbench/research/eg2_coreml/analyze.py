"""Compare native Air outputs with retained BF16 and previous Core ML references."""
import argparse
import hashlib
import json
from pathlib import Path

import numpy as np


def normalize(a): return a/np.linalg.norm(a,axis=-1,keepdims=True)
def summary(a,b):
    a,b=normalize(a.astype(np.float64)),normalize(b.astype(np.float64))
    c=np.sum(a*b,axis=-1)
    return {"count":len(c),"meanCosine":float(c.mean()),"minCosine":float(c.min()),"p01Cosine":float(np.quantile(c,.01)),"maxAbs":float(np.abs(a-b).max())}
def sha(path):return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    p=argparse.ArgumentParser()
    p.add_argument("--bf16",type=Path,required=True)
    p.add_argument("--nemosine",type=Path,required=True)
    p.add_argument("--head",type=Path,required=True)
    a=p.parse_args()
    own=json.loads(Path("evidence/cold-vectors.json").read_text())
    ix=np.array(own["indices"]);v=np.array(own["vectors"])
    assert np.isfinite(v).all() and (np.linalg.norm(v,axis=1)>0).all()
    n,d=np.fromfile(a.bf16,dtype='<u4',count=2)
    b=np.fromfile(a.bf16,dtype='<f4',offset=8).reshape(n,d)[ix]
    u=np.fromfile(a.head,dtype='<f4').astype(np.float64)
    u/=np.linalg.norm(u)
    projected=lambda x:normalize(x-np.outer(x@u,u))
    community=json.loads(a.nemosine.read_text())
    assert own["indices"]==community["indices"]
    rows=json.loads(Path('tokens.json').read_text())
    excluded=sorted(set(range(1500))-set(own['indices']))
    assert all(len(rows[i*2]['tokens'])>1024 for i in excluded)
    out={"retainedBF16ReferenceDate":"2026-11-05; historical reference, not regenerated",
         "sourceFiles":{str(x):sha(x) for x in [a.bf16,a.nemosine,a.head]},
         "finite":True,"zeroVectors":0,"normRange":[float(np.linalg.norm(v,axis=1).min()),float(np.linalg.norm(v,axis=1).max())],
         "excludedOver1024":[{"documentIndex":i,"tokens":len(rows[i*2]['tokens'])} for i in excluded],
         "stockRawVsRetainedBF16":summary(v,b),
         "sameDownstreamHeadProjectedVsRetainedBF16":summary(projected(v),projected(b)),
         "stockRawVsNemosineReference":summary(v,np.array(community["vectors"]))}
    assert out["stockRawVsRetainedBF16"]["minCosine"]>.999
    Path("evidence/corpus-parity.json").write_text(json.dumps(out,indent=2)+"\n")
    print(json.dumps(out,indent=2))


if __name__=="__main__":main()
