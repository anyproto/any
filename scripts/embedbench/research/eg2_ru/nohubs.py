"""Prespecified sensitivity: remove only authored topical non-answer hubs."""
from evaluate import *
def run(split):
 out=BASE/'results'/f'{split}-nohubs.json';assert not out.exists()
 eg=load_bank(split,'eg2');qw=load_bank(split,'qwen');ds,qs=dataset(split);ds=[r for r in ds if not r['id'].endswith('-hub')];d=vectors(eg,ds,'doc');q=vectors(eg,qs,'query');dirs=dict(np.load(BASE/'dev/directions.npz'));qh=head(q)
 scores={'stock':q@d.T,'head':qh@head(d).T,'centered':norm(q-.5*dirs['centering_mean'])@d.T,'qwen':vectors(qw,qs,'query')@vectors(qw,ds,'doc').T}
 for mode in ['plain','gloss','latin','translit']:scores['head-'+mode]=qh@head(vectors(eg,[dict(r,text=rewrite(r['text'],mode)) for r in ds],'doc')).T
 met={n:metrics(s,ds,qs) for n,s in scores.items()};save(out,summarize(met,qs));np.savez_compressed(BASE/'results'/f'{split}-nohubs-per-query.npz',**{n+'/'+k:v for n,m in met.items() for k,v in m.items()})
 log(f'{split}: eight no-authored-hub sensitivity variants saved; no changes to selected directions/doses. Dev main 42 + sensitivity 8 = 50 retrieval passes; lexical/reverse diagnostics separate.')
if __name__=='__main__':
 ap=argparse.ArgumentParser();ap.add_argument('split',choices=['dev','test']);run(ap.parse_args().split)
