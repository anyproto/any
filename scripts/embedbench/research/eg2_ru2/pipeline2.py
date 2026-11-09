"""Frozen source glossary and lexical expansion. No retrieval-dependent rules."""
from util2 import *
import re,unicodedata,functools,difflib
from collections import Counter,defaultdict
from uniseg.wordbreak import words
G=json.loads((CODE/'dictionary.json').read_text())
def loan_score(g):
 table=dict(zip('абвгдеёжзийклмнопрстуфхцчшщъыьэюя',['a','b','v','g','d','e','e','zh','z','i','i','k','l','m','n','o','p','r','s','t','u','f','h','ts','ch','sh','sh','','y','','e','yu','ya']))
 lemma=re.sub('(ить|ать|ять|еть)$','',g['lemma']);latin=''.join(table.get(c,c) for c in lemma)
 return difflib.SequenceMatcher(None,latin,g['en'].replace(' ','').replace('-','')).ratio() if g['en'] else 0
ACTIVE={i for i,g in enumerate(G) if g['en'] and loan_score(g)>=.45}
STOP=set('и или а но не ни на в во из до от по к ко с со у за для при без под над это этот эта эти как что где когда почему какой какая какие который которые есть был была быть его ее их мы вы они он она оно все уже только ещё еще также либо более менее очень например т е то же так такой такая использование помощью относящийся некоторый некоторого'.split())
TECH=re.compile(r'(?i)\b(?:код\w*|программ\w*|сервер\w*|клиент\w*|файл\w*|баз[аыуе]|базы|данн\w*|сборк\w*|тест\w*|релиз\w*|ветк\w*|репозитор\w*|компьютер\w*|приложени\w*|запрос\w*|функци\w*|интерфейс\w*|процесс\w*|библиотек\w*|ci|api|http|git|sql)\b')
def folded(s):return unicodedata.normalize('NFKC',s).casefold().replace('\u0301','')
@functools.lru_cache(maxsize=150000)
def base_tokens(s):return tuple(w for w in words(folded(s)) if any(c.isalnum() for c in w))
def forms(s):
 """Finite dictionary lookup forms, not stemming of indexed original tokens."""
 out={s};noun=['','а','у','е','ом','ы','ов','ам','ами','ах']
 if re.fullmatch('[а-яё-]+',s):
  root=s[:-1] if s.endswith(('а','я','ь','й')) else s
  if s.endswith('а'):noun=['а','ы','е','у','ой','ою','','ам','ами','ах']
  if s.endswith('я'):noun=['я','и','е','ю','ей','ею','ь','ям','ями','ях']
  if s.endswith(('ь','й')):noun=['ь','й','я','ю','е','ем','и','ей','ям','ями','ях']
  if len(root)>=3 and not s.endswith(('ать','ить','ять','еть')):out.update(root+end for end in noun)
  if s.endswith(('ать','ить','ять','еть')):
   root=s[:-3]
   ends={'ать':['ать','ал','ала','али','аю','ают','ает','аешь','ай','айте','аем'], 'ить':['ить','ил','ила','или','ит','ят','ю','и','им','ите','ишь'], 'ять':['ять','ял','яла','яли','яю','яют','яет','яешь','яй','яйте','яем'], 'еть':['еть','ел','ела','ели','ею','еют','еет','еешь','ей','ейте','еем']}[s[-3:]]
   out.update(pre+root+end for pre in ['','по','за','пере','от','до','на'] for end in ends)
  if s.endswith(('ый','ий','ой')):
   out.update(s[:-2]+end for end in ['ый','ий','ой','ая','яя','ое','ее','ые','ие','ого','его','ому','ему','ым','им','ыми','ими','ых','их','ую','юю'])
 return out
LOOK=defaultdict(set)
for i,g in enumerate(G):
 if i not in ACTIVE:continue
 for t in forms(g['lemma']):LOOK[t].add(i)
def direct(text):
 ts=base_tokens(text);ix=set(i for t in ts for i in LOOK.get(t,()))
 # Every ambiguous item requires an independently observed technical cue.
 tech=bool(TECH.search(text));return sorted(i for i in ix if not G[i]['ambiguous'] or tech)
def doc_matches(text):
 if len(text)>600 or not re.search('[а-яё]',text,re.I):return []
 return direct(text)
def vector_text(text):
 ix=doc_matches(text)
 if not ix:return text
 parts=[];used=0
 for i in ix:
  g=G[i];meaning=' '.join(g['meaning'].split()[:18]);s=meaning+(' ('+g['en']+')' if g['en'] else '')
  if len(parts)>=2 or used+len(s)>240:break
  parts.append(s);used+=len(s)
 return text+(' | Значение терминов: '+'; '.join(parts) if parts else '')
# Reverse lookup: source meaning phrases, never authored paraphrases. Require two
# non-stop meaning words, or the entire one-word meaning. Avoid generic singleton
# anchors. Index all inflections of each anchor, do not stem BM25 original tokens.
ANCHOR=defaultdict(set);NEED={};ENLOOK=defaultdict(set)
CONTENT={i:{w for w in base_tokens(g['meaning']) if len(w)>=4 and w not in STOP and re.fullmatch('[а-яё]+',w)} for i,g in enumerate(G) if i in ACTIVE}
DF=Counter(w for aa in CONTENT.values() for w in aa)
for i,g in enumerate(G):
 if i not in ACTIVE:continue
 aa={w for w in CONTENT[i] if DF[w]<=max(3,len(ACTIVE)*.02)}
 NEED[i]=max(2,int(np.ceil(len(aa)*.5))) if len(aa)>1 else 1 if len(CONTENT[i])==1 else 999
 for w in aa:
  for form in forms(w):ANCHOR[form].add((i,w))
 if g['en'] and re.fullmatch('[a-z-]+',g['en']):ENLOOK[g['en']].add(i)
def inverse(text):
 found=defaultdict(set)
 for t in set(base_tokens(text)):
  for i,w in ANCHOR.get(t,()):found[i].add(w)
 candidates=[i for i,v in found.items() if len(v)>=NEED[i]]
 candidates.sort(key=lambda i:(-len(found[i]),len(G[i]['meaning']),G[i]['lemma']))
 return candidates[:2]
def expanded_tokens(text,role):
 original=list(base_tokens(text))
 if not re.search('[а-яё]',text,re.I):return original # scoped Russian expansion
 ix=doc_matches(text) if role=='doc' else direct(text)
 if role=='query' or (len(text)<=600 and TECH.search(text)):ix=sorted(set(ix+inverse(text)))
 if not ix:return original
 extra=[]
 for i in ix[:4]:
  g=G[i];extra += list(base_tokens(g['lemma']+' '+g['en']+' '+g['meaning']))
 # Preserve originals, append each missing expansion token once.
 return original+sorted(set(extra)-set(original))
def stem_tokens(text):
 import snowballstemmer
 if not hasattr(stem_tokens,'stemmer'):stem_tokens.stemmer=snowballstemmer.stemmer('russian')
 return [stem_tokens.stemmer.stemWord(w.replace('ё','е')) if re.search('[а-яё]',w) else w for w in base_tokens(text)]
def lexical(ds,qs,mode):
 dt=[Counter(expanded_tokens(d['text'],'doc') if mode=='expand' else stem_tokens(d['text']) if mode=='stem' else base_tokens(d['text'])) for d in ds]
 qt=[expanded_tokens(q['text'],'query') if mode=='expand' else stem_tokens(q['text']) if mode=='stem' else base_tokens(q['text']) for q in qs]
 n=len(dt);length=np.array([sum(d.values()) for d in dt]);den=1.2*(.25+.75*length/max(length.mean(),1));vocab={t for q in qt for t in q};post=defaultdict(list)
 for j,d in enumerate(dt):
  for t,tf in d.items():
   if t in vocab:post[t].append((j,tf))
 cache={}
 for t,items in post.items():
  ids=np.array([j for j,tf in items]);tf=np.array([tf for j,tf in items]);df=len(ids)
  cache[t]=(ids,np.log(1+(n-df+.5)/(df+.5))*tf*2.2/(tf+den[ids]))
 out=np.zeros((len(qs),n),np.float32)
 for i,q in enumerate(qt):
  for t in set(q):
   if t in cache:
    ids,score=cache[t];out[i,ids]+=score
 return out
def fuse(v,b):
 out=np.zeros_like(v)
 for score,positive in [(v,False),(b,True)]:
  top=np.argsort(-score,axis=1,kind='stable')[:,:30]
  for i,t in enumerate(top):
   good=score[i,t]>0 if positive else np.ones(len(t),bool)
   out[i,t[good]]+=1/(60+np.arange(1,len(t)+1)[good])
 return out
if __name__=='__main__':
 import argparse
 ap=argparse.ArgumentParser();ap.add_argument('--role',choices=['doc','query'],default='doc');a=ap.parse_args()
 for line in sys.stdin:
  if not line.strip():continue
  r=json.loads(line);t=r['text'];r['embedding_text']=vector_text(t) if a.role=='doc' else t;r['lexical_tokens']=expanded_tokens(t,a.role);print(json.dumps(r,ensure_ascii=False))
