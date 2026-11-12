"""Gate the export forward against the pinned stock Transformers forward."""
import argparse
import json
from pathlib import Path

import numpy as np
import torch
import torch.nn.functional as F
from tokenizers import Tokenizer
from transformers.models.embedding_gemma2.configuration_embedding_gemma2 import EmbeddingGemma2TextConfig
from transformers.models.embedding_gemma2.modeling_embedding_gemma2 import EmbeddingGemma2TextModel

from eg2 import EG2, stock_state
from fetch_stock import verify
from provenance import fingerprint


def stats(a, b):
    a, b = np.asarray(a, dtype=np.float64), np.asarray(b, dtype=np.float64)
    return {"cosine": float(np.dot(a, b) / np.linalg.norm(a) / np.linalg.norm(b)),
            "max_abs": float(np.max(np.abs(a-b))), "finite": bool(np.isfinite(a).all() and np.isfinite(b).all())}


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--stock", type=Path, default=Path("stock"))
    a = p.parse_args()
    verify(a.stock)
    torch.set_num_threads(4)
    cfg = EmbeddingGemma2TextConfig(**json.loads((a.stock / "config.json").read_text())["text_config"])
    cfg._attn_implementation = "eager"
    hf = EmbeddingGemma2TextModel(cfg).eval()
    hf.load_state_dict(stock_state(a.stock), strict=True)
    tok = Tokenizer.from_file(str(a.stock / "tokenizer.json"))
    texts = ["title: none | text: ", "task: search result | query: How do bond prices respond to interest rates?",
             "title: Привет | text: Москва, Berlin, 東京 — مرحبا بالعالم. Ελληνικά, हिन्दी, café 🚀.",
             "title: Financial report | text: " + "The borrower repays the loan and interest. " * 68,
             "task: search result | query: " + "雪が降っています。 金融市場。 " * 55]
    cases = []
    for text in texts:
        ids = tok.encode(text).ids
        if ids[0] != 2: ids.insert(0, 2)
        if ids[-1] != 1: ids.append(1)
        length = next(x for x in [128, 256, 512, 1024] if x >= len(ids))
        cases.append({"text": text, "tokens": ids, "length": length})
    # Same short input in every bucket, plus exact-length stress and realistic long rows.
    cases += [dict(cases[1], length=x) for x in [256, 512, 1024]]
    rows = json.loads(Path("tokens.json").read_text())
    # Retained regression: all-FP16 final L2 produced norm 0.99876078
    # for FiQA document52 on the M2 Air. It must be exercised before corpus.
    cases.append(dict(rows[52*2],length=512))
    for lower, length in [(128,256),(256,512),(512,1024)]:
        row = next(r for r in rows[:3000:2] if lower < len(r["tokens"]) <= length)
        cases.append(dict(row, length=length))
    report, reference = [], []
    for length in [128,256,512,1024]:
        own = EG2(a.stock, length).eval()
        for n, case in enumerate(cases):
            if case["length"] != length: continue
            ids = torch.zeros((1,length), dtype=torch.long)
            ids[0,:len(case["tokens"])] = torch.tensor(case["tokens"])
            mask = torch.zeros((1,length));mask[0,:len(case["tokens"])]=1
            with torch.inference_mode():
                h = hf(input_ids=ids, attention_mask=mask).last_hidden_state
                expected = F.normalize((h*mask[:,:,None]).sum(1)/mask.sum(1,keepdim=True), dim=-1)[0].numpy()
                actual = own(ids.to(torch.int32),mask)[0].numpy()
            s = dict(case=n,length=length,tokens=len(case["tokens"]),**stats(expected,actual))
            print(json.dumps(s),flush=True);report.append(s)
            reference.append(dict(case=n,**case,vector=expected.tolist()))
        del own
    out=Path("evidence");out.mkdir(exist_ok=True)
    passed=all(r["finite"] and r["cosine"]>0.9999999 and r["max_abs"]<2e-5 for r in report)
    assert any(r["length"]==1024 and r["tokens"]>513 for r in report)
    (out/"torch-parity.json").write_text(json.dumps({"passed":passed,"provenance":fingerprint(),"reference":"Transformers 5.19.0 stock weights, FP32 eager, raw masked mean then L2", "cases":report},indent=2)+"\n")
    (out/"hf-reference.json").write_text(json.dumps(reference)+"\n")
    assert passed, "Stock forward parity failed"


if __name__ == "__main__": main()
