#!/usr/bin/env python3
"""Frozen document-side glossary expansion; stdlib-only, JSONL stdin/stdout.

Input original JSON records with a `text` field. Output keeps that field intact and
adds `embedding_text`, `glossary_expanded`. Embed embedding_text with the unchanged
production document prefix; show and cite the original text. This experiment's
40-term dictionary is a research resource, not a universal Russian normalizer.
"""
import argparse,json,re,sys
from pathlib import Path

def expand(text,glossary):
 additions=[]
 for g in glossary:
  if g['en'] in ['master branch','pull request'] and not re.search(r'ветк|код|коммит|мерж|апрув|изменени|сборк|релиз|ревью',text,re.I):continue
  if re.search(r'(?<!\w)(?:'+g['pattern']+r')(?!\w)',text,re.I):additions.append(g['ru']+' ('+g['en']+')')
 return text+(' | Значение терминов: '+'; '.join(dict.fromkeys(additions)) if additions else '')
def main():
 ap=argparse.ArgumentParser(description=__doc__);ap.add_argument('--glossary',type=Path,default=Path(__file__).with_name('glossary.json'));a=ap.parse_args();gloss=json.loads(a.glossary.read_text())
 for line in sys.stdin:
  if not line.strip():continue
  r=json.loads(line);t=expand(r['text'],gloss);r.update(embedding_text=t,glossary_expanded=t!=r['text']);print(json.dumps(r,ensure_ascii=False))
if __name__=='__main__':main()
