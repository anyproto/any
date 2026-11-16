"""Sanitize completed full-app evidence; exclude every config/account/credential file."""
import argparse,hashlib,json,re,shutil,urllib.parse
from pathlib import Path
import yaml
p=argparse.ArgumentParser();p.add_argument('--raw',type=Path,required=True);p.add_argument('--output',type=Path,required=True);a=p.parse_args()
assert not a.output.exists(),'Use a fresh evidence destination'
a.output.mkdir()
config=yaml.safe_load(Path('staging-nodeconf.yml').read_text())
values=set()
def walk(v):
 if isinstance(v,dict):
  for x in v.values():walk(x)
 elif isinstance(v,list):
  for x in v:walk(x)
 elif isinstance(v,str) and len(v)>8:
  values.add(v)
  if '.' in v:
   host=urllib.parse.urlparse(v if '://' in v else 'tcp://'+v).hostname
   if host and '.' in host:values.add(host)
walk(config)
def sanitize(s):
 for v in sorted(values,key=len,reverse=True):s=s.replace(v,'[STAGING_VALUE]')
 # Additional stage hosts can arrive through nodeconf updates.
 s=re.sub(r'(?i)\b[\w.-]*stag(?:e|ing)[\w.-]*\.(?:org|io|com)(?::\d+)?','[STAGING_HOST]',s)
 # A control handshake must never appear even if a future logger changes.
 s=re.sub(r'CONTROL_TOKEN\s+[0-9a-fA-F]+','CONTROL_TOKEN [REDACTED]',s)
 return s
for path in sorted(a.raw.rglob('*')):
 if not path.is_file():continue
 assert path.suffix in {'.json','.jsonl','.txt','.log'},path
 target=a.output/path.relative_to(a.raw);target.parent.mkdir(parents=True,exist_ok=True)
 text=path.read_text(errors='replace')
 if path.name=='app.log':
  text='\n'.join(line for line in text.splitlines() if any(key in line.lower() for key in ['local embedder','neural engine','index.model_download']))+'\n'
 target.write_text(sanitize(text))
for name in ['harness.py','inputs.json','model-package.json']:
 shutil.copyfile(name,a.output/name)
shutil.copyfile('evidence/binary-build.txt',a.output/'binary-build.txt')
manifest=[]
for path in sorted(a.output.rglob('*')):
 if path.is_file():manifest.append(hashlib.sha256(path.read_bytes()).hexdigest()+'  '+str(path.relative_to(a.output)))
(a.output/'SHA256SUMS').write_text('\n'.join(manifest)+'\n')
print(json.dumps({'files':len(manifest),'directory':str(a.output),'networkConfigIncluded':False,'credentialsIncluded':False},indent=2))
