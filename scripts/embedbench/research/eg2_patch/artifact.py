"""Patch copied GGUFs in-place; preserve all metadata, names, shapes and types.

Untouched Q8 blocks remain byte-identical to ggml-org's production artifact.
Edited Q8 tensors are quantized from the rounded patched BF16 values with the
gguf-py reference Q8_0 quantizer. No repeated requantization of unchanged tensors.
"""
from common import *
import gguf,shutil,argparse

def publish(spec,stem,directions=None):
    # Read only the small matrices actually being edited; no inference/model load.
    from edit_math import build_edit
    from types import SimpleNamespace
    src=ROOT/'bench/embeddinggemma-2-BF16.gguf';r=gguf.GGUFReader(str(src))
    names={s['tensor'] for s in spec['sites']}
    orig={t.name:gguf.dequantize(t.data,t.tensor_type).copy() for t in r.tensors if t.name in names}
    edits,details=build_edit(SimpleNamespace(original=orig),spec,np.load(directions or BASE/'dev/directions.npz'))
    manifest={'spec':spec,'tensors':details,'files':{}}
    for dtype in ['BF16','Q8_0']:
        source=ROOT/'bench'/f'embeddinggemma-2-{dtype}.gguf';dest=BASE/'out'/f'{stem}-{dtype}.gguf'
        assert not dest.exists(),f'Refusing to overwrite frozen artifact {dest}'
        shutil.copyfile(source,dest);rr=gguf.GGUFReader(str(dest),'r+')
        for t in rr.tensors:
            if t.name in edits:
                q=gguf.quantize(edits[t.name],t.tensor_type);assert q.shape==t.data.shape;t.data[:]=q
        rr.data.flush();del rr
        sr=gguf.GGUFReader(str(source));dr=gguf.GGUFReader(str(dest));diff=[]
        for a,b in zip(sr.tensors,dr.tensors):
            assert a.name==b.name and np.array_equal(a.shape,b.shape) and a.tensor_type==b.tensor_type
            if not np.array_equal(a.data,b.data):
                assert a.name in edits
                diff.append(dict(name=a.name,type=a.tensor_type.name,base_sha256=hashlib.sha256(a.data.tobytes()).hexdigest(),patched_sha256=hashlib.sha256(b.data.tobytes()).hexdigest()))
        assert set(d['name'] for d in diff)==names
        first=min(t.data_offset for t in sr.tensors)
        with source.open('rb') as a,dest.open('rb') as b:assert a.read(first)==b.read(first),'Header/metadata changed'
        manifest['files'][dtype]=dict(path=str(dest.relative_to(ROOT)),sha256=sha(dest),source_sha256=sha(source),different_tensors=diff)
    save(BASE/'out'/f'{stem}-manifest.json',manifest)
    log(f'Published frozen {stem} BF16 and Q8_0; only specified tensors differ, header/metadata bit-identical; SHA256 manifest saved.')
    return manifest

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('spec');p.add_argument('stem');p.add_argument('--directions');a=p.parse_args()
    publish(json.loads(Path(a.spec).read_text()),a.stem,a.directions)
