import hashlib
import json
from pathlib import Path

import coremltools as ct
from coremltools.proto.FeatureTypes_pb2 import ArrayFeatureType


def sha(p):
    h=hashlib.sha256()
    with p.open('rb') as f:
        for b in iter(lambda:f.read(8*1024*1024),b''):h.update(b)
    return h.hexdigest()


root=Path('models');merged=root/'EmbeddingGemma2Owned.mlpackage'
spec=ct.utils.load_spec(str(merged))
blobs={}
matrix_ops={}
for name,fn in spec.mlProgram.functions.items():
    seen=set()
    matrix_count=0
    for block in fn.block_specializations.values():
        for op in block.operations:
            if op.type in {'conv','matmul','linear'}:
                assert all(output.type.tensorType.dataType==10 for output in op.outputs), 'Encoder matrix operation is not FP16'
                matrix_count+=1
            for val in op.attributes.values():
                if val.WhichOneof('value')=='blobFileValue':
                    seen.add((val.blobFileValue.fileName,val.blobFileValue.offset))
    blobs[name]=seen
    matrix_ops[name]=matrix_count
assert set(blobs)=={'s128','s256','s512','s1024'}
contracts={}
for function in spec.description.functions:
    length=int(function.name[1:])
    inputs={feature.name:(list(feature.type.multiArrayType.shape),feature.type.multiArrayType.dataType) for feature in function.input}
    outputs={feature.name:(list(feature.type.multiArrayType.shape),feature.type.multiArrayType.dataType) for feature in function.output}
    assert inputs=={'input_ids':([1,length],ArrayFeatureType.INT32),'attention_mask':([1,length],ArrayFeatureType.FLOAT32)}
    assert outputs=={'embedding':([1,768],ArrayFeatureType.FLOAT32)}
    contracts[function.name]={'inputs':inputs,'outputs':outputs}
packages={}
for p in sorted(root.glob('*.mlpackage')):
    files={str(f.relative_to(p)):{'bytes':f.stat().st_size,'sha256':sha(f)} for f in sorted(p.rglob('*')) if f.is_file()}
    packages[p.name]={'bytes':sum(v['bytes'] for v in files.values()),'files':files}
out={'source':json.loads(Path('source-manifest.json').read_text()),
     'conversionProvenance':json.loads((root/'s128.provenance.json').read_text()),
     'packages':packages,'functions':sorted(blobs),'contracts':contracts,'verifiedFP16MatrixOperations':matrix_ops,'externalTensorReferences':{k:len(v) for k,v in blobs.items()},
     'sharedExternalTensorOffsetsAcrossAllFunctions':len(set.intersection(*blobs.values())),
     'allDistinctExternalTensorOffsets':len(set.union(*blobs.values())),
     'standaloneTotalBytes':sum(v['bytes'] for k,v in packages.items() if k.startswith('s')),
     'mergedBytes':packages[merged.name]['bytes']}
assert out['mergedBytes']<out['standaloneTotalBytes']*.4
Path('evidence/asset-manifest.json').write_text(json.dumps(out,indent=2)+'\n')
print(json.dumps({k:v for k,v in out.items() if k not in ['packages','source','conversionProvenance']},indent=2))
