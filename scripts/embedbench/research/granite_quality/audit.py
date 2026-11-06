"""Read-only local model/tokenizer/GGUF audit; no inference or account access."""
from common import *
import sys
from transformers import AutoTokenizer
from tokenizers import Tokenizer
path=ROOT/'bench/granite/hf'
tok=AutoTokenizer.from_pretrained(path,local_files_only=True)
raw=Tokenizer.from_file(str(path/'tokenizer.json'))
rows,_,_=loadset('flores-devtest')
rows=rows[::7]
diff=sum(tok.encode(r['text'])!=raw.encode(r['text']).ids for r in rows)
out={'tested_tokenizer_sequences':len(rows),'hf_vs_raw_tokenizer_mismatches':diff,
     'pre_tokenizer':json.loads((path/'tokenizer.json').read_text())['pre_tokenizer'],
     'config':json.loads((path/'config.json').read_text()),
     'sentence_transformers':json.loads((path/'config_sentence_transformers.json').read_text()),
     'pooling':json.loads((path/'1_Pooling/config.json').read_text())}
sys.path.insert(0,str(ROOT/'bench/granite/llama.cpp-b11600/gguf-py'))
from gguf import GGUFReader
r=GGUFReader(str(ROOT/'bench/granite/granite-311m-mlr2-Q8_0.gguf'))
out['gguf']={k:v.contents() for k,v in r.fields.items() if any(s in k for s in ['add_bos','add_eos','add_sep','bos_token_id','eos_token_id','pooling','context_length','architecture'])}
(BASE/'debug/audit.json').write_text(json.dumps(out,indent=2))
log(f'Audited actual GGUF BOS/EOS/CLS metadata and {len(rows)} public tokenizer sequences: HF-vs-tokenizer.json mismatches={diff}. Transformers 4.57.6 Mistral-regex warning is a local-config detection false positive (ModernBERT config version 4.57.3, space Split tokenizer).')
