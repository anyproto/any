#!/usr/bin/env python3
"""Disposable staging-only full-app check. Never reads an existing account."""
import argparse,datetime,hashlib,json,os,queue,re,shutil,signal,subprocess,sys,threading,time,urllib.request,urllib.error
from pathlib import Path
p=argparse.ArgumentParser();p.add_argument('--root',type=Path,required=True);p.add_argument('--binary',type=Path,required=True);p.add_argument('--lib-dir',type=Path);p.add_argument('--nodeconf',type=Path,required=True);p.add_argument('--smoke',action='store_true');p.add_argument('--ready-timeout',type=int,default=1200);a=p.parse_args()
a.root=a.root.resolve();a.binary=a.binary.resolve();here=Path(__file__).resolve().parent
assert not a.root.exists(),'Use a fresh isolated test root'
a.root.mkdir(mode=0o700);os.umask(0o077)
evidence=a.root/'evidence';private=a.root/'private';data=a.root/'data'
for d in [evidence,private,data]:d.mkdir(mode=0o700)
config={'dataDir':str(data),'mode':'managed','listen':{'addr':'127.0.0.1:0'},'webUI':{'enabled':False},'network':{'nodeconfPath':str(a.nodeconf.resolve())},'sync':{'dialTimeout':'1s'},'p2p':{'enabled':False,'localDiscovery':False,'global':{'enabled':False,'relayUrls':[],'pkarrRelayUrls':[]}},'push':{'enabled':False},'index':{'enabled':True,'embedder':'none' if a.smoke else 'local','embedBatch':8,'embedConcurrency':1,'local':{'model':'embeddinggemma-2','neuralEngine':'auto','contextSize':2048,'batchDocs':1,'libDir':str(a.lib_dir.resolve()) if a.lib_dir else ''}}}
config_path=a.root/'config.json';config_path.write_text(json.dumps(config,indent=2)+'\n')
inputs=json.loads((here/'inputs.json').read_text());contract=json.loads((here/'model-package.json').read_text())
log_path=evidence/'app.log';events_path=evidence/'events.jsonl';stdout_q=queue.Queue();secret_values=[];control=None;base=None;child=None;proc=None;account=None
result={'dataset':'synthetic mixed-length pages/queries sharing a topic; flow validation, not retrieval-quality evaluation','phase':'starting','sourceCommit':'b7d2c5ee9002713d2e4dc9f6d0a8d8fbd9be095a','smoke':a.smoke,'target':'16GiB M2 Air; no8GiB claim' if not a.smoke else 'local FTS-only setup smoke','inputs':[{'bucket':r['bucket'],'promptedTokens':r['expectedPromptedTokens']} for r in inputs]}
def write(name,value):
 temp=evidence/(name+'.tmp');temp.write_text(json.dumps(value,indent=2)+'\n');temp.replace(evidence/name)
def event(phase,**kw):
 entry={'utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'monotonic':time.monotonic(),'phase':phase,**kw}
 with events_path.open('a') as f:f.write(json.dumps(entry)+'\n')
 print(json.dumps(entry),flush=True)
def request(method,path,body=None,timeout=60):
 req=urllib.request.Request(base+path,data=None if body is None else json.dumps(body).encode(),method=method,headers={'Content-Type':'application/json','X-Any-Control-Token':control or ''})
 try:
  with urllib.request.urlopen(req,timeout=timeout) as r:
   raw=r.read();return json.loads(raw) if raw else None
 except urllib.error.HTTPError as e:
  # Failures are never printed as raw bodies.
  payload=e.read();error=json.loads(payload) if payload else {};raise RuntimeError(f'{method} {path}: HTTP{e.code} '+str(error.get('error',{}).get('code','')))
def drain_stdout(pipe):
 with (private/'stdout.txt').open('w') as f:
  for line in iter(pipe.readline,''):
   f.write(line);f.flush();stdout_q.put(line.strip())
def children():
 raw=subprocess.check_output(['/bin/ps','-axo','pid,ppid,command'],text=True)
 found=[]
 for line in raw.splitlines()[1:]:
  fields=line.strip().split(None,2)
  if len(fields)==3 and fields[2].startswith(str(a.binary)+' run embedder '):found.append(int(fields[0]))
 return found
def snapshot(tag):
 ids=[proc.pid]+children();state={'tag':tag,'parent':proc.pid,'children':ids[1:]}
 for pid in ids:
  out=evidence/f'footprint-{tag}-{pid}.txt'
  with out.open('w') as f:
   r=subprocess.run(['/usr/bin/footprint','--wired',str(pid)],stdout=f,stderr=subprocess.STDOUT)
  state[str(pid)]={'footprintExit':r.returncode}
 write('snapshot-'+tag+'.json',state);event('snapshot',tag=tag,children=state['children']);return state

def search(row):
 start=time.monotonic();r=request('POST','/v1/spaces/'+space+'/search',{'query':row['query'],'mode':'vector','limit':10},timeout=240)
 elapsed=time.monotonic()-start
 event('vector-query',bucket=row['bucket'],tokens=row['expectedPromptedTokens'],seconds=elapsed,vectorStatus=r.get('vectorStatus'),hits=len(r.get('hits',[])))
 return r,elapsed
try:
 env={k:v for k,v in os.environ.items() if not k.startswith(('ANY_','OPENAI_','HF_','HUGGING_FACE_','OLLAMA_','YZMA_'))}
 log=log_path.open('w');proc=subprocess.Popen([str(a.binary),'run','--config',str(config_path),'--addr','127.0.0.1:0'],stdout=subprocess.PIPE,stderr=log,text=True,env=env,start_new_session=True)
 threading.Thread(target=drain_stdout,args=(proc.stdout,),daemon=True).start()
 deadline=time.monotonic()+60
 while base is None or control is None:
  if proc.poll() is not None:raise RuntimeError('App exited before handshake')
  if time.monotonic()>deadline:raise RuntimeError('App handshake timed out')
  try:line=stdout_q.get(timeout=.5)
  except queue.Empty:continue
  if line.startswith('LISTENING '):base='http://'+line.split(' ',1)[1]
  if line.startswith('CONTROL_TOKEN '):control=line.split(' ',1)[1];secret_values.append(control)
 health=request('GET','/v1/health');assert health['networkId']=='N9DU6hLkTAbvcpji3TCKPPd3UQWKGyzUxGmgJEyvhByqAjfD';write('initial-health.json',health)
 event('listening',pid=proc.pid,address=base,networkId=health['networkId'])
 auth=request('POST','/v1/auth',{},timeout=120);account=auth['accountId'];secret_values.append(auth.get('mnemonic',''));(private/'auth.json').write_text(json.dumps(auth))
 assert (private/'auth.json').stat().st_mode&0o777==0o600
 event('fresh-account-created',created=auth.get('created'))
 # Use only this disposable account's derived staging space; never join another account's space.
 sp=request('POST','/v1/spaces/derived/bao',{},timeout=90);space=sp['id'];write('space.json',{k:sp.get(k) for k in ['id','name','status','derived']})
 object_ids=[]
 for row in inputs:
  ob=request('POST','/v1/spaces/'+space+'/objects',{'type':'page','initialProperties':{'any':{'name':row['title']}}},timeout=90)
  oid=ob['objectId'];object_ids.append(oid)
  request('PUT','/v1/spaces/'+space+'/objects/'+oid+'/editor/editor_blocks/markdown',{'content':row['content']},timeout=90)
 event('documents-created',count=len(object_ids));write('object-ids.json',object_ids)
 deadline=time.monotonic()+90
 while True:
  fts=request('POST','/v1/spaces/'+space+'/search',{'query':'lifecycle verification','mode':'fts','limit':10})
  if len({x['objectId'] for x in fts.get('hits',[])})==len(inputs):break
  if time.monotonic()>deadline:raise RuntimeError('FTS did not observe all created documents')
  time.sleep(1)
 write('fts.json',fts);event('fts-ready',documents=len(inputs))
 if a.smoke:
  assert not list((data/'models').glob('*')), 'FTS smoke unexpectedly downloaded a model'
  result.update(phase='complete',stagingAccountSpaceAndIndexPassed=True)
 else:
  assert a.lib_dir and a.lib_dir.is_dir()
  # Fresh data root proves the app's pinned public downloader is exercised.
  deadline=time.monotonic()+a.ready_timeout;last_event=0;last_process_snapshot=None
  while True:
   processes=request('GET','/v1/processes');write('processes-download-latest.json',processes)
   serialized=json.dumps(processes,sort_keys=True)
   if serialized!=last_process_snapshot:
    observation={'utc':datetime.datetime.now(datetime.timezone.utc).isoformat(),'monotonic':time.monotonic(),'processes':processes.get('processes',[])}
    with (evidence/'processes-download.jsonl').open('a') as f:f.write(json.dumps(observation)+'\n')
    last_process_snapshot=serialized
   candidates=list((data/'models').glob('*.mlpackage'))
   complete=[d for d in candidates if all((d/name).is_file() for name in contract['files'])]
   if complete:break
   if time.monotonic()>deadline:raise RuntimeError('Public package download timed out')
   if time.monotonic()-last_event>20:event('waiting-public-download',modelDirectories=len(candidates));last_event=time.monotonic()
   time.sleep(2)
  package=complete[0];observed={}
  for name,expected in contract['files'].items():
   path=package/name;h=hashlib.sha256()
   with path.open('rb') as f:
    for chunk in iter(lambda:f.read(8*1024*1024),b''):h.update(chunk)
   assert h.hexdigest()==expected['sha256'],name
   observed[name]={'sha256':h.hexdigest(),'bytes':path.stat().st_size}
  gguf=data/'models/embeddinggemma-2-Q8_0.gguf';gh=hashlib.sha256()
  with gguf.open('rb') as f:
   for chunk in iter(lambda:f.read(8*1024*1024),b''):gh.update(chunk)
  assert gh.hexdigest()=='2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135'
  write('downloaded-gguf.json',{'path':str(gguf),'bytes':gguf.stat().st_size,'sha256':gh.hexdigest()})
  write('downloaded-package.json',{'path':str(package),'files':observed,'hfRevision':'c27f9012633055fea5bb47fe782ab7d548d77909'});event('public-package-verified',bytes=sum(x['bytes'] for x in observed.values()))
  deadline=time.monotonic()+a.ready_timeout
  while True:
   for row in inputs:
    try:search(row)
    except RuntimeError as e:
     if 'HTTP503' not in str(e):raise
     event('warmup-query-unavailable',bucket=row['bucket'],error=str(e))
   text=log_path.read_text(errors='replace');ready=[row['bucket'] for row in inputs if re.search(r's'+str(row['bucket'])+r' ready',text)]
   event('warming-functions',ready=ready)
   if len(ready)==5:break
   if 'not used:' in text or 'neural engine: off' in text:raise RuntimeError('App neural path rejected a function')
   if time.monotonic()>deadline:raise RuntimeError('App functions did not all become ready')
   time.sleep(5)
  # Finish indexing before the intentionally quiet interval.
  deadline=time.monotonic()+120
  while True:
   state=request('GET','/v1/processes');running=[x for x in state.get('processes',[]) if x.get('kind','').startswith('index.') and x.get('state')=='running']
   if not running:break
   if time.monotonic()>deadline:raise RuntimeError('Indexing did not quiesce')
   for row in inputs:search(row)
   time.sleep(3)
  measured=[]
  for _ in range(2):
   for row in inputs:
    response,elapsed=search(row);assert response.get('vectorStatus')=='used' and response.get('hits')
    assert all(__import__('math').isfinite(x['score']) for x in response['hits'])
    assert any(x['objectId'] in object_ids for x in response['hits']), 'No created document retrieved'
    measured.append({'bucket':row['bucket'],'seconds':elapsed,'vectorStatus':response['vectorStatus'],'hits':len(response['hits'])})
  write('http-vector-timing.json',measured)
  active=snapshot('active');assert len(active['children'])==1;child=active['children'][0]
  idle_start=time.monotonic();event('idle-start',child=child)
  # No HTTP embeddings or writes during either idle interval.
  while time.monotonic()-idle_start<65:time.sleep(max(0,min(5,65-(time.monotonic()-idle_start))))
  one=snapshot('idle-65s');text=log_path.read_text(errors='replace')
  released=[n for n in [256,512,1024,2048] if f's{n} released after 1m0s unused' in text]
  assert len(released)==4 and one['children']==[child],f'One-minute release check: {released}, child={one["children"]}'
  event('one-minute-release-passed',released=released)
  while time.monotonic()-idle_start<315:time.sleep(max(0,min(5,315-(time.monotonic()-idle_start))))
  five=snapshot('idle-315s');assert child not in five['children'] and not five['children'],'Embedding child did not retire'
  assert 'local embedder child retired while idle' in log_path.read_text(errors='replace')
  event('five-minute-child-exit-passed');result.update(phase='complete',downloadVerified=True,oneMinuteRelease=True,fiveMinuteChildExit=True,activeChild=child)
except Exception as e:
 result.update(phase='failed',error=str(e));event('failed',error=str(e))
finally:
 if proc is not None and proc.poll() is None:
  try:request('POST','/v1/shutdown',{},timeout=40)
  except Exception:proc.send_signal(signal.SIGTERM)
  try:proc.wait(timeout=50)
  except subprocess.TimeoutExpired:os.killpg(proc.pid,signal.SIGKILL);proc.wait();result['forcedKill']=True
  result['appExitCode']=proc.returncode
 if proc is not None:
  remaining=[]
  for pid in set(children())|({child} if child else set()):
   try:os.kill(pid,0);remaining.append(pid)
   except ProcessLookupError:pass
  result['remainingOwnedChildren']=remaining
  if remaining:
   result.update(phase='failed',error='Owned embedding child survived shutdown')
   for pid in remaining:
    try:os.kill(pid,signal.SIGTERM)
    except ProcessLookupError:pass
   time.sleep(2)
   for pid in children():
    try:os.kill(pid,signal.SIGKILL)
    except ProcessLookupError:pass
  result['ownedChildrenAfterCleanup']=children()
 if 'log' in locals():log.close()
 # Preserve public cache/evidence, erase the disposable identity and SDK account state.
 for entry in list(data.iterdir()):
  if entry.name!='models':
   if entry.is_dir():shutil.rmtree(entry)
   else:entry.unlink()
 if private.exists():shutil.rmtree(private)
 for path in [log_path]:
  if path.exists():
   text=path.read_text(errors='replace')
   for value in secret_values:
    if value:text=text.replace(value,'[REDACTED]')
   path.write_text(text)
 result['temporaryCredentialsRemoved']=True;write('result.json',result);event('finished',result=result['phase'],appExitCode=result.get('appExitCode'))
if result['phase']!='complete':sys.exit(1)
