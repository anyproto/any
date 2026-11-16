"""Compare matched long-input native timings and outputs, retaining every sample."""
import json,hashlib
from pathlib import Path
import numpy as np

def read(n):return json.loads(Path('evidence',n).read_text())
a,m=read('ane-warm.json'),read('metal-warm.json');inputs=read('benchmark-inputs.json')
metal_log=Path('evidence/air-metal-warm.log').read_text()
assert 'offloaded 25/25 layers to GPU' in metal_log and 'assigned to device MTL0' in metal_log
assert 'calling encode() instead' not in metal_log
assert m['api']=='llama_encode (encoder-only EG2)' and m['memoryClearData'] is False
assert a['phase']=='complete' and a['computeUnits']=='cpuAndNeuralEngine'
assert a['warmupRounds']==m['warmupRounds']==2 and a['measuredRounds']==m['measuredRounds']==5
assert [r['id'] for r in a['rows']]==[r['id'] for r in m['rows']]==[r['id'] for r in inputs]
rows=[]
for x,y,src in zip(a['rows'],m['rows'],inputs):
    assert x['tokens']==y['tokens']==len(src['tokens']) and 1024<x['tokens']<=2048
    u,v=np.array(x['vector']),np.array(y['vector']);nu,nv=np.linalg.norm(u),np.linalg.norm(v)
    assert u.shape==v.shape==(768,) and np.isfinite(u).all() and np.isfinite(v).all() and abs(nu-1)<.001 and abs(nv-1)<.001
    cosine=float(u@v/nu/nv);assert cosine>.998,'Q8/FP16 parity gate failed'
    am,mm=float(np.median(x['samplesMs'])),float(np.median(y['samplesMs']))
    rows.append(dict(id=x['id'],tokens=x['tokens'],aneMedianMs=am,metalMedianMs=mm,aneSpeedupVsMetal=mm/am,cosine=cosine,maxAbs=float(np.abs(u-v).max()),aneNorm=float(nu),metalNorm=float(nv)))
out=dict(inputsSHA256=hashlib.sha256(Path('evidence/benchmark-inputs.json').read_bytes()).hexdigest(),warmupRounds=2,measuredRounds=5,rows=rows,
  metalPlacement='All25/25 layers offloaded to MTL0 per retained native llama.cpp load log.',
  metalAPI=m['api'],metalMemoryPresent=m['memoryPresent'],metalMemoryClearData=m['memoryClearData'],
  precisionCaveat='Direct-runtime comparison (not exact production wrappers): Core ML FP16 encoder + FP32 L2 versus stock Q8_0 GGUF. Different precisions and Swift/Go host APIs; not a precision-controlled backend study.',
  timingBoundary='Same complete pretokenized inputs, batch1, normalized copied768-vector. Input construction, synchronous inference and output copying/normalization included; tokenizer/model load excluded. Backend-specific state cleanup included. No downstream head projection.',
  aneTimingBoundary=a['timingBoundary'],metalTimingBoundary=m['timingBoundary'])
for name,bench in [('ane',a),('metal',m)]:
    values=np.array([x for r in bench['rows'] for x in r['samplesMs']]);out[name]=dict(count=len(values),meanMs=float(values.mean()),p50Ms=float(np.median(values)),p95Ms=float(np.quantile(values,.95)),totalMs=float(values.sum()))
out['aggregateSpeedupVsMetal']=out['metal']['totalMs']/out['ane']['totalMs']
Path('evidence/matched-latency.json').write_text(json.dumps(out,indent=2)+'\n');print(json.dumps(out,indent=2))
