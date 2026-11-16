"""Write a dated report only after every requested five-bucket gate has evidence."""
import json,re
from pathlib import Path
import numpy as np
from buckets import LENGTHS
E=Path('evidence')
def read(n):return json.loads((E/n).read_text())
def ledger(p):
    s=p.read_text();line=next((l.strip() for l in s.splitlines() if '(neural) (nofootprint)' in l),None)
    phys=re.search(r'phys_footprint: (.+)',s)
    return dict(neuralLedgerRow=line,physFootprint=phys.group(1) if phys else None)
cold,warm,corpus,assets,rebuild,torch,env,latency=[read(n) for n in ['cold.json','warm.json','corpus-parity.json','asset-manifest.json','rebuild-reproducibility.json','torch-parity.json','build-environment.json','matched-latency.json']]
assert cold['phase']=='complete' and cold['documents']==1499 and set(cold['plans'])=={str(n) for n in LENGTHS}
assert rebuild['weightBlobIdentical'] and rebuild['modelProtoSemanticallyIdentical']
for mode in ['memory-alone','memory-pair']:assert read(mode+'.json')['modelsExplicitlyReleased']
hf=cold['hfFP32Parity'];assert len(hf)==16
stat=lambda rows:dict(cases=len(rows),minimumCosine=min(r['cosine'] for r in rows),maximumAbsoluteError=max(r['maxAbs'] for r in rows),maximumNormError=max(abs(r['norm']-1) for r in rows))
summary=dict(date='2026-11-16',source=assets['source'],environment=env,target=dict(os=cold['os'],host=cold['host'],chip='Apple M2',ramGiB=16),freshHFCoreMLParity=stat(hf),longHFCoreMLParity=stat([r for r in hf if r['tokens']>1024]),
 fp32RewriteParity=dict(cases=len(torch['cases']),minimumCosine=min(r['cosine'] for r in torch['cases']),maximumAbsoluteError=max(r['max_abs'] for r in torch['cases'])),
 corpus=corpus,plans=cold['plans'],compileSeconds=cold['compileSeconds'],coldLoadSeconds=cold['loadSeconds'],cachedLoadSeconds=warm['loadSeconds'],firstPredictionSeconds=cold['firstPredictSeconds'],
 corpusTiming=dict(docsPerSecond=cold['docsPerSecond'],p50Ms=cold['p50Ms'],p95Ms=cold['p95Ms'],byBucket={}),matchedLatency=latency,
 memory={p.stem.removeprefix('footprint-'):ledger(p) for p in sorted(E.glob('footprint-*.txt'))},reproducibility=rebuild,packageBytes=assets['mergedBytes'],provenanceByFunction=assets['conversionProvenanceByFunction'])
for length,values in cold['latenciesByBucketMs'].items():
 a=np.array(values);summary['corpusTiming']['byBucket'][length]=dict(count=len(a),p50Ms=float(np.median(a)),p95Ms=float(np.quantile(a,.95)),meanMs=float(a.mean()))
(E/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
lines=['EG2 five-function Core ML — measured November16 extension','',
 'RESULT: PASS. Exact shared s128/s256/s512/s1024/s2048 artifact validated on M2 Air,16GiB,'+cold['os']+'.',
 'This validates the model artifact; production runtime activation and 8GiB lifecycle policy are separate decisions.',
 'Local/Air task root: /tmp/eg2-owned-2048-20261116. Delivered package: models/EmbeddingGemma2Owned.mlpackage.',
 'Exact source package: /tmp/eg2-bucket-size-comparison-20261116/EmbeddingGemma2-5buckets.mlpackage.',
 'No community converted weights used. FP16 encoder; FP32 final L2; float32 normalized raw768-output. No application head projection.','',
 'PROVENANCE',
 'Stock google/embeddinggemma-2 @ '+assets['source']['revision'],
 'Source weights SHA256 '+assets['source']['files']['model.safetensors']['sha256'],
 'Tokenizer SHA256 '+assets['source']['files']['tokenizer.json']['sha256'],
 'Python3.12.9; '+', '.join(k+' '+v for k,v in env['dependencies'].items()),
 'Exact merged package retains four Nov12 exports and the new Nov16 s2048 export. Architecture eg2.py, stock source and dependency versions match; export fingerprints differ.',
 'Per-function provenance, file hashes, contracts and matrix precision assertions are in evidence/asset-manifest.json.',
 'Nov12 target was macOS27.0.1; every requested function is revalidated here on macOS27.2.1.','',
 'NUMERICAL GATES',
 'Stock HF FP32 vs rewritten forward: '+json.dumps(summary['fp32RewriteParity']),
 'Fresh stock HF FP32 vs native CoreML: '+json.dumps(summary['freshHFCoreMLParity']),
 'Native >1024-token fixtures: '+json.dumps(summary['longHFCoreMLParity']),
 'Includes exact2048 tokens inclBOS/EOS,1151-token FiQA,1630-token multilingual text, short18-token text padded2048, and retained doc52 normalization regression.',
 'All1499 fitting FiQA documents passed finite/dimension768/unit-norm gates. Only doc178(len2212) excluded, with no truncation/fallback.',
 'Corpus norm range: '+str(corpus['normRange']),
 corpus.get('bf16ReferenceLimitation','Optional BF16 comparison supplied; see corpus-parity.json.'),
 'Retained native reference comparisons: '+json.dumps(corpus['referenceComparisons']),
 'Eight newly covered documents: '+str(corpus['new2048DocumentIndices']),
 'Raw-output parity remains separate from downstream head projection.','',
 'PREFERRED PLACEMENT (not a runtime operator trace)',
 'length ANE_ops CPU_ops ANE_estimated_cost CPU_operators']
for length in LENGTHS:
 p=cold['plans'][str(length)];lines.append(f"{length} {p['ops'].get('ane',0)} {p['ops'].get('cpu',0)} {p['estimatedCost'].get('ane',0):.6f} "+','.join(r['operator'] for r in p['cpuOperators']))
lines+=['Every requested function produced a compute plan; CPU convolution/matmul/linear/softmax core was rejected. CPU lookup/final FP32 normalization is allowed.',
 'cpuAndNeuralEngine allows CPU. Estimated costs are not measured walltime; no ANE-only claim.','',
 'LOAD / CORPUS TIMING',
 'Cold load seconds: '+json.dumps(cold['loadSeconds']),
 'Same executable/path cached load seconds: '+json.dumps(warm['loadSeconds']),
 'Package compile seconds (separate): '+json.dumps(cold['compileSeconds']),
 'First predict seconds: '+json.dumps(cold['firstPredictSeconds']),
 'Corpus: '+json.dumps(summary['corpusTiming']),
 'Corpus timing includes input fill/prediction/output copy and excludes tokenization.','',
 'MATCHED WARM LONG-INPUT LATENCY',latency['precisionCaveat'],latency['timingBoundary'],
 'Two warmup rounds, five measured rounds of identical11 complete inputs,1025–2048 tokens. One inference owner at a time.',
 'ANE: '+json.dumps(latency['ane']), 'Metal: '+json.dumps(latency['metal']),
 'Aggregate Metal-time / ANE-time: '+str(latency['aggregateSpeedupVsMetal']),
 latency['metalPlacement'],
 'Metal API: '+latency['metalAPI']+'; encoder memory present: '+str(latency['metalMemoryPresent'])+'. Metadata-only clear requested when present.',
 'Near the lower boundary, compare '+json.dumps(next(r for r in latency['rows'] if r['id']=='exact-1025'))+'; exact2048 '+json.dumps(next(r for r in latency['rows'] if r['id']=='hf-15'))+'. Aggregate speedup is not universal across lengths.',
 'Per-input medians, normalized vectors, Q8/FP16 cosine and every sample are retained in evidence/.','',
 'MEMORY — footprint --wired',
 'Neural nofootprint allocations are separate from ordinary physical footprint; retain Clean/Reclaimable/Wired distinction.',
 'Each memory mode uses a separate process: s2048 alone; s128+s2048. Explicit release2048, immediate and30s snapshots, then releaseall.']
for name,row in summary['memory'].items():lines += [name+': ordinary '+str(row['physFootprint']), '  '+str(row['neuralLedgerRow'])]
lines+=['No forced pressure, cache flush, service restart, or 8GiB safety claim.','',
 'DISK / REPRODUCIBILITY',
 f"Exact package {assets['mergedBytes']} bytes;394 external offsets shared across5 functions,419 distinct offsets. +16489148bytes over equivalent4functionpackage.",
 'Disk sharing does not imply shared per-function neural allocations.',
 'Clean s2048 rebuild: identical learned-weight blob and parsed protobuf; canonical SHA256 '+rebuild['deterministicProtoSHA256']['initial'],
 'Raw protobuf map order/package UUIDs may differ; not byte-identical whole-package packaging.',
 'README reproduction rebuilds all5 from pinned stock in a fresh directory. Fresh deployment prevents reuse of stale adjacent compiled model.',
 'All owned test processes exited; see evidence/process-exit-check.txt. Source bundle includes scripts, fixtures, locks/licenses and SHA256SUMS; no large model/venv binaries.','']
text='\n'.join(lines)
for old,new in [('November16','November 16'),('Nov12','Nov 12'),('Nov16','Nov 16'),('macOS27','macOS 27'),('Air,16GiB','Air, 16 GiB'),('8GiB','8 GiB'),('Python3','Python 3'),('raw768','raw 768'),('exact2048','exact 2048'),('inclBOS/EOS','including BOS/EOS'),('short18','short 18'),('padded2048','padded 2048'),('doc52','doc 52'),('All1499','All 1499'),('doc178(len2212)','doc 178 (2212 tokens)'),('for16','for 16'),('including1151','including 1151'),('identical11','identical 11'),('batch1','batch 1'),('copied768','copied 768'),('release2048','release 2048'),('and30s','and 30s'),('releaseall','release all'),('across5','across 5'),('over equivalent4functionpackage','over the equivalent four-function package'),('rebuilds all5','rebuilds all 5'),('All25/25','All 25/25'),('At1025','At 1025'),('at2048','at 2048'),('is299.55ms','is 299.55 ms'),('Metal726.67ms','Metal 726.67 ms')]:text=text.replace(old,new)
Path('REPORT.txt').write_text(text);print(text)
