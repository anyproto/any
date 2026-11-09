"""Meaningful protocol/math checks before freezing; no held-out scoring."""
from util2 import *
from pipeline2 import forms,vector_text,expanded_tokens,base_tokens,lexical,fuse
from data2 import clusters
from run2 import metrics
def run():
 verify_seal()
 assert 'сидя' not in forms('сид') and 'ника' in forms('ник') and 'бэкапили' in forms('бэкапить')
 assert list(base_tokens('Ёлка елка'))==['ёлка','елка']
 assert vector_text('Рейс задержался. Мокрый чемодан остался в самолёте.')=='Рейс задержался. Мокрый чемодан остался в самолёте.'
 assert expanded_tokens('The flight was delayed.','doc')==list(base_tokens('The flight was delayed.'))
 ds=[dict(id='a',text='alpha'),dict(id='b',text='beta')];qs=[dict(id='q',text='unseen')];rels={'q':{'a':1}}
 b=lexical(ds,qs,'base');m=metrics(b,ds,qs,rels,positive=True)
 assert b.max()==0 and m['mrr10'][0]==0 and m['rank'][0]==3
 f=fuse(np.array([[.1,.9]],np.float32),b);assert np.argmax(f[0])==1
 cs=clusters([dict(id='1'),dict(id='2'),dict(id='3')],{'1':{'a':1},'2':{'a':1,'b':1},'3':{'b':1}});assert len(set(cs))==1
 a=np.array([1,0,1,0]);assert ci(a,a,['a','a','b','b'])['lo']==0
 save(BASE/'dev/protocol-checks.json',dict(seal=True,declension=True,base_analyzer=True,ambiguity=True,no_hit_metrics=True,positive_only_lexical_fusion=True,target_connected_bootstrap=True))
 log('Protocol/math checks passed: sealed integrity, noun forms, ambiguity no-op, zero-hit exclusion, positive lexical fusion and target-connected bootstrap.')
if __name__=='__main__':run()
