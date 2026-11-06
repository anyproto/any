"""Standalone research figure; all intervals are exploratory dev intervals."""
from common import *
sys.path.insert(0,str(BASE/'deps'))
os.environ['MPLCONFIGDIR']=str(BASE/'cache/matplotlib')
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

def read_variant(name):return json.loads((BASE/'dev'/f'{name}.json').read_text())
def points(names):
    rows=[read_variant(n) for n in names]
    rows=[(r['spec']['alpha'],r['delta']['ndcg10']) for r in rows]
    rows.append((0,dict(delta=0.,lo=0.,hi=0.)))
    rows.sort(key=lambda x:x[0])
    return np.array([x for x,_ in rows]),np.array([r['delta'] for _,r in rows]),np.array([[r['delta']-r['lo'] for _,r in rows],[r['hi']-r['delta'] for _,r in rows]])

fig,axes=plt.subplots(1,2,figsize=(10,4),layout='constrained')
series=[('All MLP columns',['dose-iso-23-mlp-res--0.5','dose-iso-23-mlp-res-0.1','dose-iso-23-mlp-res-0.25','dose-iso-23-mlp-res-0.5','iso-23-mlp-res']),
        ('Selected 32 columns',['writer-reverse','writer-half','sparse-32-0.5','sparse-32-1.0'])]
for label,names in series:
    x,y,e=points(names);axes[0].errorbar(x,y,yerr=e,marker='o',capsize=3,label=label)
r=read_variant('writer-random')['delta']['ndcg10'];axes[0].errorbar([.5],[r['delta']],yerr=[[r['delta']-r['lo']],[r['hi']-r['delta']]],fmt='s',color='gray',capsize=3,label='Norm-matched random')
x,y,e=points([f'head-mean-{a}' for a in [-.5,.25,.5,1.,1.5]])
axes[1].errorbar(x,y,yerr=e,marker='o',capsize=3,label='Dev document mean')
r=read_variant('head-random')['delta']['ndcg10'];axes[1].errorbar([1],[r['delta']],yerr=[[r['delta']-r['lo']],[r['hi']-r['delta']]],fmt='s',color='gray',capsize=3,label='Norm-matched random')
for ax,title in zip(axes,['Last MLP writer','Separate output-head projection']):
    ax.axhline(0,color='black',lw=.8);ax.set_xlabel('Projection strength α');ax.set_ylabel('Δ nDCG@10 from original EG2');ax.set_title(title);ax.grid(alpha=.2);ax.legend(fontsize=8)
fig.suptitle('Development only: 20 photosynthesis queries\nPaired 95% bootstrap intervals; adaptive sweep, no multiplicity correction',fontsize=11)
fig.savefig(BASE/'dev/dose-response.png',dpi=180);fig.savefig(BASE/'dev/dose-response.svg');plt.close(fig)
log('Saved dev-only dose-response PNG/SVG with paired intervals and matched random controls.')
