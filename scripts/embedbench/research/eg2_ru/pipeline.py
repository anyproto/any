from util import *
import re,unicodedata
from collections import Counter
from uniseg.wordbreak import words
import snowballstemmer
GLOSS=json.loads((BASE/'glossary.json').read_text())
STEM=snowballstemmer.stemmer('russian')
def rewrite(text,mode):
 if mode=='original':return text
 additions=[]
 def sub(m,g):
  # Avoid public-relations пиар and non-technical master senses.
  if g['en'] in ['master branch','pull request'] and not re.search(r'ветк|код|коммит|мерж|апрув|изменени|сборк|релиз|ревью',text,re.I):return m[0]
  if mode=='translit':return transliterate(m[0])
  if mode=='latin':return g['en']
  if mode=='plain':return g['ru']
  additions.append(g['ru']+' ('+g['en']+')');return m[0]
 for g in GLOSS:text=re.sub(r'(?<!\w)(?:'+g['pattern']+r')(?!\w)',lambda m:sub(m,g),text,flags=re.I)
 return text+(' | Значение терминов: '+'; '.join(dict.fromkeys(additions)) if additions else '')
def tokens(s,mode='base'):
 if mode=='gloss':s=rewrite(s,'gloss')
 s=unicodedata.normalize('NFKC',s).casefold()
 ts=[w for w in words(s) if any(c.isalnum() for c in w)]
 if mode in ['stem','gloss']:ts=[STEM.stemWord(w.replace('ё','е')) if re.search('[а-яё]',w) else w for w in ts]
 return ts

def bm25(ds,qs,mode):
 dt=[Counter(tokens(d['text'],mode)) for d in ds];qt=[tokens(q['text'],'stem' if mode=='gloss' else mode) for q in qs]
 df=Counter(t for d in dt for t in d);n=len(dt);length=np.array([sum(x.values()) for x in dt]);avg=length.mean();den=1.2*(.25+.75*length/avg)
 vocab=set(t for q in qt for t in q);cache={}
 for t in vocab:
  tf=np.array([d.get(t,0) for d in dt]);cache[t]=np.log(1+(n-df[t]+.5)/(df[t]+.5))*tf*2.2/(tf+den)
 return np.array([sum((cache[t] for t in set(q)),start=np.zeros(n)) for q in qt],dtype=np.float32)
def fuse(v,b,fetch=30):
 out=np.zeros_like(v)
 for score in [v,b]:
  top=np.argsort(-score,axis=1,kind='stable')[:,:fetch]
  for i,t in enumerate(top):
   good=np.ones(len(t),bool) if score is v else score[i,t]>0
   out[i,t[good]]+=1/(60+np.arange(1,len(t)+1)[good])
 return out

def transliterate(s):
 table=dict(zip("абвгдеёжзийклмнопрстуфхцчшщъыьэюя",["a","b","v","g","d","e","yo","zh","z","i","y","k","l","m","n","o","p","r","s","t","u","f","kh","ts","ch","sh","shch","","y","","e","yu","ya"]))
 return "".join(table.get(c,c) for c in s.lower())
