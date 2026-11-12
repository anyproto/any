"""Download the pinned public checkpoint, then verify every source byte."""
import hashlib
import json
import urllib.request
from pathlib import Path


def verify(root=Path("stock")):
    manifest=json.loads(Path("source-manifest.json").read_text())
    for name, expected in manifest["files"].items():
        p=root/name
        h=hashlib.sha256()
        with p.open("rb") as f:
            for chunk in iter(lambda:f.read(8*1024*1024),b""):h.update(chunk)
        if p.stat().st_size!=expected["bytes"] or h.hexdigest()!=expected["sha256"]:
            raise ValueError(f"Stock file integrity failed: {p}")
    return manifest


if __name__=="__main__":
    manifest=json.loads(Path("source-manifest.json").read_text())
    root=Path("stock");root.mkdir(exist_ok=True)
    for name, expected in manifest["files"].items():
        p=root/name
        if not p.exists():
            url=f'https://huggingface.co/{manifest["repo"]}/resolve/{manifest["revision"]}/{name}'
            temporary=p.with_suffix(p.suffix+".partial")
            print("Downloading",name,flush=True)
            urllib.request.urlretrieve(url,temporary)
            temporary.replace(p)
    print(json.dumps(verify(),indent=2))
