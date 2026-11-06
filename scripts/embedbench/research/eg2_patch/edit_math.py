"""Closed-form NumPy/GGUF edit recipe; no inference or MLX dependency."""
from common import *

def build_edit(model, spec, dirs):
    edits={};details=[]
    for site in spec['sites']:
        name=site['tensor'];w=np.array(model.original[name]);u=dirs[site['direction']].copy()
        cols=np.arange(w.shape[1]) if site.get('k',0)==0 else dirs[site['columns']][:site['k']]
        du=np.outer(u,u@w[:,cols])
        if spec.get('random'):
            rng=np.random.default_rng(391+int(site.get('layer',0)));v=rng.normal(size=len(u));v=norm(v-u*(v@u))
            dv=np.outer(v,v@w[:,cols]);dv*=np.linalg.norm(du)/np.linalg.norm(dv);du=dv
        delta_w=spec['alpha']*du
        new=w.copy();new[:,cols]-=delta_w
        # Round to BF16 exactly as the artifact will be written, using gguf-py.
        import gguf
        new=gguf.dequantize(gguf.quantize(new,gguf.GGMLQuantizationType.BF16),gguf.GGMLQuantizationType.BF16)
        edits[name]=new
        details.append(dict(tensor=name,columns=cols.tolist(),changed_entries=int(np.count_nonzero(w!=new)),relative_frobenius=float(np.linalg.norm(new-w)/np.linalg.norm(w)),delta_norm=float(np.linalg.norm(new-w))))
    return edits,details

