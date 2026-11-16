"""Preserve full prompt/token inputs for matched native ANE and Metal timings."""
import json,hashlib
from pathlib import Path
from tokenizers import Tokenizer
rows=json.loads(Path('tokens.json').read_text());refs=json.loads(Path('evidence/hf-reference.json').read_text())
cases=[dict(id=f'fiqa-{i}',text=r['text'],tokens=r['tokens'],kind='real-fiqa') for i,r in enumerate(rows[:3000:2]) if 1024<len(r['tokens'])<=2048]
for r in refs:
    if r['case'] in (14,15):cases.append(dict(id=f'hf-{r["case"]}',text=r['text'],tokens=r['tokens'],kind=r['kind']))
tok=Tokenizer.from_file('stock/tokenizer.json')
def encode(s):
    ids=tok.encode(s).ids
    if ids[0]!=2:ids.insert(0,2)
    if ids[-1]!=1:ids.append(1)
    return ids
prefix='title: Exact lower boundary | text:'
n=1025-len(encode(prefix))
for _ in range(8):
    text=prefix+' x'*n;ids=encode(text)
    if len(ids)==1025:break
    n+=1025-len(ids)
assert len(ids)==1025
cases.insert(0,dict(id='exact-1025',text=text,tokens=ids,kind='synthetic-full-text'))
for c in cases:
    assert 1024<len(c['tokens'])<=2048
    assert encode(c['text'])==c['tokens']
    c['tokenCount']=len(c['tokens'])
Path('evidence/benchmark-inputs.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2)+'\n')
print([(c['id'],c['tokenCount']) for c in cases])
print(hashlib.sha256(Path('evidence/benchmark-inputs.json').read_bytes()).hexdigest())
