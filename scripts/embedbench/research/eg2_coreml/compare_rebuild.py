"""Bounded clean-build check; distinguish model bytes from package UUIDs."""
import hashlib
import json
from pathlib import Path
import coremltools as ct
import argparse
from buckets import LENGTHS, MAX_LENGTH


def sha(p):
    h=hashlib.sha256()
    with p.open('rb') as f:
        for b in iter(lambda:f.read(8*1024*1024),b''):h.update(b)
    return h.hexdigest()


p=argparse.ArgumentParser();p.add_argument('--length',type=int,choices=LENGTHS,default=MAX_LENGTH);args=p.parse_args()
initial=Path(f'models/s{args.length}.mlpackage');rebuilt=Path(f'reproducibility-rebuild/s{args.length}.mlpackage')
files={}
for p in initial.rglob('*'):
    if p.is_file():
        relative=p.relative_to(initial);q=rebuilt/relative
        a,b=sha(p),sha(q)
        files[str(relative)]={'initialSHA256':a,'rebuiltSHA256':b,'equal':a==b}
out={'length':args.length,'freshOutputDirectory':str(rebuilt),'files':files,
     'weightBlobIdentical':files['Data/com.apple.CoreML/weights/weight.bin']['equal'],
     'modelProtoIdentical':files['Data/com.apple.CoreML/model.mlmodel']['equal'],
     'wholePackageFilesIdentical':all(x['equal'] for x in files.values())}
a=ct.utils.load_spec(str(initial));b=ct.utils.load_spec(str(rebuilt))
out['modelProtoSemanticallyIdentical']=a==b
out['deterministicProtoSHA256']={
    'initial':hashlib.sha256(a.SerializeToString(deterministic=True)).hexdigest(),
    'rebuilt':hashlib.sha256(b.SerializeToString(deterministic=True)).hexdigest()}
out['interpretation']='Same weight bytes and identical parsed protobuf; raw protobuf map serialization order and package UUIDs differ. Not a byte-identical whole package.'
Path('evidence/rebuild-reproducibility.json').write_text(json.dumps(out,indent=2)+'\n')
print(json.dumps(out,indent=2))
assert out['weightBlobIdentical'] and out['modelProtoSemanticallyIdentical'], 'Inspect differences and rerun numerical parity before claiming reproducibility'
