"""Source-only extraction: this script never reads scenarios or retrieval data."""
from util2 import *
import re,html,unicodedata
def clean(s):
 s=re.split(r'\{\{пример\b|<ref\b',s,maxsplit=1)[0]
 s=re.sub(r'\[\[(?:[^\]|]*\|)?([^\]]+)\]\]',r'\1',s)
 s=re.sub(r'\{\{(?:lang|сэ)\|en\|([^|}]+)(?:\|[^}]*)?\}\}',r'\1',s)
 s=s.replace('{{-}}',' — ')
 for _ in range(8):s=re.sub(r'\{\{[^{}]*\}\}',' ',s)
 s=re.sub('<[^>]+>',' ',s);s=html.unescape(s).replace("'",'').replace('\u0301','')
 return re.sub(r'\s+',' ',s).strip(' ;,.:—-')
def english(s):
 pats=[r'\{\{(?:lang|сэ)\|en\|([^|}]+)',r'\{\{lang-en\|([^|}]+)',r'англ\.\s*(?:\[\[)?([A-Za-z][A-Za-z -]{1,40})',r'\{\{этимология:([A-Za-z][A-Za-z -]{1,40})\|']
 for p in pats:
  for x in re.findall(p,s):
   x=clean(x).strip(' -').lower()
   if re.fullmatch('[a-z][a-z -]{1,39}',x):return x
 return ''
def main():
 verify_seal();assert not (BASE/'FROZEN.json').exists();pages=read(BASE/'sources/pages.jsonl');entries={};rejected=[]
 for p in pages:
  title=p['title'];w=p['wikitext']
  if title.startswith('Приложение:'):continue
  if not re.fullmatch('[а-яё-]{3,30}',title):continue
  m=re.search(r'^=\s*\{\{-ru-\}\}\s*=\s*$(.*?)(?=^=\s*\{\{-(?!ru-)|\Z)',w,re.S|re.M)
  if not m:continue
  ru=m[1];senses=[];all_senses=[]
  for block in re.findall(r'====\s*Значение\s*====(.*?)(?=\n===|\Z)',ru,re.S):
   for line in block.splitlines():
    if re.match(r'^#(?![#:*])',line):
     raw=line[1:].strip();text=clean(raw)
     if text:all_senses.append(text)
     if re.search(r'\{\{(?:комп\.|прогр\.|программир\.|информ\.|интернет|компьют)',raw,re.I) and text:senses.append(text)
  if not senses:
   rejected.append(dict(title=title,reason='No explicitly computer-labelled sense'));continue
  # Keep etymology within the homonym section of the selected computer sense.
  sections=re.split(r'^==\s+[^=\n].*?\s+==\s*$',ru,flags=re.M)
  section=next((sec for sec in sections if any(clean(line[1:].strip())==senses[0] for line in sec.splitlines() if re.match(r'^#(?![#:*])',line))),ru)
  et=' '.join(re.findall(r'===\s*Этимология\s*===(.*?)(?=\n===|\Z)',section,re.S));en=english(et)
  text=senses[0];text=re.split(r'\s*◆',text)[0]
  if len(text)<4 or '{{' in text:continue
  entries[title]=dict(lemma=title,en=en,meaning=text,ambiguous=len(all_senses)>len(senses) or len(senses)>1,source=p['url'],revision=p['revid'],history=p['history'],extraction='first explicitly computer-labelled sense; first explicit English etymon',source_text=et[:1000])
 appendix=next(p for p in pages if p['title'].startswith('Приложение:'))
 for line in appendix['wikitext'].splitlines():
  if not re.match(r'^\*[^*]',line):continue
  line=clean(line[1:]);m=re.match(r'^([А-Яа-яЁё][А-Яа-яЁё -]{1,60}?)(?:\s*\((.*?)\))?\s*[—–]\s*(.+)',line)
  if not m:continue
  names,et,meaning=m.groups();en=english(et or '')
  if not en:continue  # appendix: require a documented English loanword
  for lemma in [x.strip().lower() for x in names.split(',')]:
   if not re.fullmatch('[а-яё-]{3,30}',lemma) or lemma in entries:continue
   entries[lemma]=dict(lemma=lemma,en=en,meaning=meaning,ambiguous=True,source=appendix['url'],revision=appendix['revid'],history=appendix['history'],extraction='appendix entry with explicit English etymon; conservative ambiguity gate',source_text=et)
 # No per-term supplements or hand-authored paraphrases.
 result=sorted(entries.values(),key=lambda g:g['lemma'])
 save(CODE/'dictionary.json',result);save(BASE/'sources/extraction.json',dict(entries=len(result),with_english=sum(bool(g['en']) for g in result),ambiguous=sum(g['ambiguous'] for g in result),rejected=rejected,source_sha256=sha(BASE/'sources/pages.jsonl')))
 log(f'Extracted {len(result)} dictionary entries ({sum(bool(g["en"]) for g in result)} English etymons) mechanically from published pages; no sealed data consulted.')
if __name__=='__main__':main()
