import hashlib
import importlib.metadata
import json
import platform
from pathlib import Path


def fingerprint(conversion=False):
    names=["eg2.py","parity.py","fetch_stock.py","source-manifest.json","provenance.py"]
    if conversion:names.append("convert.py")
    data={"files":{n:hashlib.sha256(Path(n).read_bytes()).hexdigest() for n in names},
          "versions":{n:importlib.metadata.version(n) for n in ["torch","transformers","coremltools","numpy","safetensors","tokenizers"]},
          "python":platform.python_version()}
    data["fingerprint"]=hashlib.sha256(json.dumps(data,sort_keys=True).encode()).hexdigest()
    return data
