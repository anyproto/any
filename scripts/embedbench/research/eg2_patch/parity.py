"""Compare inference outputs to the production harness vector files."""
from common import *
import argparse
p=argparse.ArgumentParser();p.add_argument('python');p.add_argument('harness');p.add_argument('--out',required=True);a=p.parse_args()
x=norm(np.load(a.python));ys=[]
for role in ['docs','queries']:
    f=Path(a.harness+'.'+role+'.f32');shape=np.fromfile(f,dtype='<u4',count=2)
    ys.append(np.fromfile(f,dtype='<f4',offset=8).reshape(shape))
y=norm(np.concatenate(ys));assert x.shape==y.shape
c=(x*y).sum(1);report=dict(min_cosine=float(c.min()),mean_cosine=float(c.mean()),cosines=c.tolist(),max_abs_error=float(abs(x-y).max()),passed=bool(c.min()>=.9999))
save(a.out,report);print(json.dumps(report,indent=2));assert report['passed']
log(f'Parity {Path(a.python).name} versus {Path(a.harness).name}: min cosine {c.min():.8f}, >=0.9999 passed.')
