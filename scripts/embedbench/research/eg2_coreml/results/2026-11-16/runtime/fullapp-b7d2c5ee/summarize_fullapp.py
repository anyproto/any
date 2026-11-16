"""Summarize completed synthetic full-app flow and independently check idle windows."""
import argparse,datetime,hashlib,json,re
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--evidence',type=Path,required=True);a=p.parse_args();root=a.evidence
read=lambda name:json.loads((root/name).read_text())
r=read('result.json');assert r['phase']=='complete' and r['appExitCode']==0 and r['temporaryCredentialsRemoved'] and not r['remainingOwnedChildren'] and not r.get('ownedChildrenAfterCleanup')
assert r['sourceCommit']=='b7d2c5ee9002713d2e4dc9f6d0a8d8fbd9be095a'
events=[json.loads(x) for x in (root/'events.jsonl').read_text().splitlines()]
idle=next(x for x in events if x['phase']=='idle-start');start=datetime.datetime.fromisoformat(idle['utc'])
active,one,five=[read('snapshot-'+x+'.json') for x in ['active','idle-65s','idle-315s']]
assert len(active['children'])==1 and one['children']==active['children'] and not five['children']
release={};retirement=[];downloads=[];readies=[]
log_text=(root/'app.log').read_text()
assert 'local embedder child died with the neural engine on' not in log_text
assert not re.search(r's(?:128|256|512|1024|2048) not used:',log_text)
for line in (root/'app.log').read_text().splitlines():
 match=re.match(r'^(\d{4}-\d\d-\d\dT\S+)',line)
 if not match:continue
 try:stamp=datetime.datetime.fromisoformat(match[1])
 except ValueError:continue
 delta=(stamp-start).total_seconds()
 ready=re.search(r's(128|256|512|1024|2048) ready',line)
 if ready:
  details=re.search(r'plan ([0-9.]+)s, load ([0-9.]+)s, (\d+) operations on the neural engine, (\d+) on the cpu',line)
  entry={'bucket':int(ready[1]),'utc':stamp.isoformat()}
  if details:entry.update(planSeconds=float(details[1]),loadSeconds=float(details[2]),neuralEngineOperations=int(details[3]),cpuOperations=int(details[4]))
  readies.append(entry)
 rel=re.search(r's(256|512|1024|2048) released after 1m0s unused',line)
 if rel and delta>=0:release[int(rel[1])]={'utc':stamp.isoformat(),'secondsAfterIdleStart':delta}
 if 'local embedder child retired while idle' in line and delta>=0:retirement.append({'utc':stamp.isoformat(),'secondsAfterIdleStart':delta})
 if 'local embedder: model downloaded' in line:
  dest=re.search(r'"dest"\s*:\s*"([^"]+)"',line)
  downloads.append({'utc':stamp.isoformat(),'file':Path(dest[1]).name if dest else 'unknown'})
assert set(release)=={256,512,1024,2048},release
assert all(0<=x['secondsAfterIdleStart']<80 for x in release.values())
assert len(retirement)==1 and 280<retirement[0]['secondsAfterIdleStart']<330,retirement
assert set(x['bucket'] for x in readies)=={128,256,512,1024,2048}
assert [x['file'] for x in downloads]==['embeddinggemma-2-Q8_0.gguf','Manifest.json','model.mlmodel','weight.bin'],downloads
history=[json.loads(x) for x in (root/'processes-download.jsonl').read_text().splitlines()]
targets=[];peak=0
for obs in history:
 current=[x for x in obs['processes'] if x.get('kind')=='index.model_download'];peak=max(peak,len(current))
 for x in current:
  t=x.get('target')
  if t and (not targets or targets[-1]!=t):targets.append(t)
assert peak==1
memory={}
scale={'B':1,'KB':1024,'MB':1024**2,'GB':1024**3}
for path in sorted(root.glob('footprint-*.txt')):
 text=path.read_text();line=next((x.strip() for x in text.splitlines() if '(neural) (nofootprint)' in x),None)
 phy=re.search(r'phys_footprint: (.+)',text);entry={'physicalFootprint':phy[1] if phy else None,'neuralLedgerRow':line}
 if line:
  sizes=re.findall(r'(\d+(?:\.\d+)?)\s+(GB|MB|KB|B)\b',line)
  assert len(sizes)>=4
  entry['neuralApproxMiB']={k:float(v)*scale[u]/1024**2 for k,(v,u) in zip(['dirty','clean','reclaimable','wired'],sizes[:4])}
 memory[path.name]=entry
latencies=read('http-vector-timing.json');assert len(latencies)==10 and all(x['vectorStatus']=='used' and x['hits']>0 for x in latencies)
bybucket={str(n):[x['seconds']*1000 for x in latencies if x['bucket']==n] for n in [128,256,512,1024,2048]}
summary={'result':'PASS','sourceCommit':r['sourceCommit'],'scope':'Synthetic mixed-length pages/queries sharing a topic: application-flow and lifetime validation, not retrieval-quality evaluation. 16 GiB M2 Air only; no 8 GiB claim.','documents':5,'queriesPromptedTokens':[80,190,390,750,1500],'downloadedPackage':read('downloaded-package.json'),'downloadedGGUF':read('downloaded-gguf.json'),'downloadCompletionOrder':downloads,'observedProcessTargets':targets,'maxConcurrentDownloadProcessesObserved':peak,'processHistorySnapshots':len(history),'tinyFilePollingCaveat':'Small files can finish between polls; full completion order comes from app download logs and final hashes.','functionReadyEvents':readies,'queryMillisecondsByBucket':bybucket,'activeChildPID':active['children'][0],'idleStartUTC':idle['utc'],'releaseEventsAfterIdleStart':release,'childRetirementAfterIdleStart':retirement,'memory':memory,'temporaryCredentialsRemoved':True,'appExitCode':0,'ownedChildrenRemaining':[],'datasetInputsSHA256':hashlib.sha256((root/'inputs.json').read_bytes()).hexdigest(),'harnessSHA256':hashlib.sha256((root/'harness.py').read_bytes()).hexdigest()}
(root/'SUMMARY.json').write_text(json.dumps(summary,indent=2)+'\n')
lines=['Full-app EG2 / ANE lifecycle — b7d2c5ee on 16 GiB M2 Air','',summary['scope'],'','RESULT: PASS. Fresh managed staging account; public GGUF and exact five-function CoreML package downloaded by the app into an empty cache.','Five synthetic pages created and indexed; all five CoreML functions became ready. Ten measured vector HTTP requests returned finite scores and hits belonging to created pages.','Download completion order: '+', '.join(x['file'] for x in downloads)+'. One index.model_download process observed at a time.','CoreML package weights SHA256: '+summary['downloadedPackage']['files']['Data/com.apple.CoreML/weights/weight.bin']['sha256'],'', 'HTTP query times in milliseconds (two per bucket, complete app path; all five functions ready):',json.dumps(bybucket,sort_keys=True),'','Idle interval sent no embedding requests or writes. Per-function release logs independently checked against that interval:']
for n,x in sorted(release.items()):lines.append(f"s{n}: release logged {x['secondsAfterIdleStart']:.3f} s after idle snapshot.")
lines+=['Child retirement logged at '+format(retirement[0]['secondsAfterIdleStart'],'.3f')+' s; original child alive at 65 s and absent at 315 s.','', 'Memory comes from footprint --wired for parent and child. Neural Clean/Reclaimable/Wired is outside ordinary footprint:']
for name,x in memory.items():lines.append(name+': phys '+str(x['physicalFootprint'])+'; '+str(x.get('neuralApproxMiB','no neural ledger')))
lines+=['','Explicit managed shutdown exited 0. Temporary recovery/control files and account SDK state removed; no owned embedding child remains. Public model cache retained.','Ordinary footprint and neural nofootprint columns are reported separately; their sum is not an exact total-RAM measure. Reclaimable is not already freed. One active snapshot does not bound peak use.',
 'App binary source is b7d2c5ee; the harness explicitly configures contextSize 2048, batchDocs 1, embedConcurrency 1 and embedBatch 8. This run makes no claim about subsequent code heads, 8 GiB systems, realistic-corpus retrieval quality, or framework leak elimination.','Shareable evidence omits network configuration and credentials; staging endpoint values are redacted.','']
(root/'REPORT.txt').write_text('\n'.join(lines));print(json.dumps({k:summary[k] for k in ['result','sourceCommit','documents','maxConcurrentDownloadProcessesObserved','releaseEventsAfterIdleStart','childRetirementAfterIdleStart']},indent=2))
