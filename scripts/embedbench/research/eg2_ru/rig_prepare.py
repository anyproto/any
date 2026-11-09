from evaluate import *
assert (BASE/'FROZEN.json').exists()
assert not (BASE/'sets/rig-docs.jsonl').exists()
rig=read(BASE/'from-rig/synthetic-lookalikes.jsonl');ds=read(BASE/'sets/public-background.jsonl')+[dict(r,formality='chat') for r in rig];qs=[];bank={}
for r in rig:
 for kind in ['kw','para']:qs.append(dict(id=r['id']+':'+kind,text=r[kind+'_query'],target=r['id'],group=r['id'],kind=kind,source_kind='rig',formality='chat',length=len(r['text']),latin_share=latin_share(r['text']),rig_reproduces_gap=r['reproduces_gap']))
for d in ds:
 for mode in ['original','plain','gloss','latin','translit']:
  t=rewrite(d['text'],mode);k=key(t,'doc');bank[k]=dict(key=k,text=t,role='doc',prompt='prod')
for q in qs:
 k=key(q['text'],'query');bank[k]=dict(key=k,text=q['text'],role='query',prompt='prod')
write(BASE/'sets/rig-docs.jsonl',ds);write(BASE/'sets/rig-queries.jsonl',qs);write(BASE/'sets/rig-bank.jsonl',list(bank.values()))
save(BASE/'results/rig-provenance.json',dict(source_sha256=sha(BASE/'from-rig/synthetic-lookalikes.jsonl'),background='same frozen 800 public StackOverflow passages',not_reproduced='1434 private rig distractors never accessed; reported rig ranks retained as external evidence only',glossary_triggers={r['id']:rewrite(r['text'],'gloss')!=r['text'] for r in rig}))
log('Opened eight approved rig synthetic look-alikes only after freeze; built 808-document public confirmation pool. Dictionary/directions unchanged; rig private background not available/read.')
