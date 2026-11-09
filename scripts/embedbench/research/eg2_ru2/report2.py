"""Render saved results and prespecified source-labelled slices; never encode/rank."""
from util2 import *
from pipeline2 import G,ACTIVE,doc_matches,vector_text,LOOK,base_tokens
from data2 import SETS
def fmt(d,n=3):return f"{d['delta']:+.{n}f} [{d['lo']:+.{n}f}, {d['hi']:+.{n}f}]"
def load(name):return json.loads((BASE/'results'/f'{name}.json').read_text())
def frozen_arms():
 d=float(json.loads((BASE/'FROZEN.json').read_text())['weight_dose']);return ['H','Q','V','L','C','S','W'+str(d),'R'+str(d)]
def rows_for(name):return read(BASE/'results'/f'{name}-queries.jsonl')
def from_arrays(name,indices):
 z=np.load(BASE/'results'/f'{name}.npz');qs=rows_for(name);groups=[qs[i].get('target',qs[i]['id']) for i in indices];out={}
 for a in frozen_arms():
  for leg in ['vector','bm25','hybrid']:
   n=a+'.'+leg;v=z[n+'/mrr10'][indices];out[n]=dict(mrr=float(v.mean()),vs_head=ci(v,z['H.'+leg+'/mrr10'][indices],groups),vs_qwen=ci(v,z['Q.'+leg+'/mrr10'][indices],groups))
 return dict(n=len(indices),scores=out)
def main():
 from analysis2 import run as reporting_audits
 reporting_audits()
 arms=frozen_arms();fr=json.loads((BASE/'FROZEN.json').read_text());sealed=load('sealed');r1=load('round1');rig=load('rig');source=json.loads((BASE/'sources/active-mapping.json').read_text());srows=read(BASE/'sets/sealed-scenarios.jsonl')
 coverage=[];known={g['lemma']:i for i,g in enumerate(G)}
 for r in srows:
  ix=doc_matches(r['text']);family=r['family'];coverage.append(dict(id=r['id'],family=family,round1_known=r['round1_known'],exact_family_in_dictionary=family in known,exact_family_active=family in known and known[family] in ACTIVE,vector_expanded=vector_text(r['text'])!=r['text'],matches=[{k:G[i][k] for k in ['lemma','en','meaning','source']} for i in ix]))
 write(BASE/'results/sealed-coverage-detail.jsonl',coverage)
 derived={}
 batch2=read(BASE/'from-rig/lookalikes-batch2.jsonl') if (BASE/'from-rig/lookalikes-batch2.jsonl').exists() else []
 if (BASE/'results/new-rig.npz').exists():
  qq=rows_for('new-rig');pattern={r['id'] for r in batch2 if r.get('set')=='pattern'};control={r['id'] for r in batch2 if r.get('set')=='control'};batchids={r['id'] for r in batch2}
  old=read(OLD/'from-rig/synthetic-lookalikes.jsonl');allpattern=pattern|{r['id'] for r in old if r.get('reproduces_gap')}
  for label,ids,kind in [('new-pattern',pattern,'para'),('new-controls',control,'para'),('new-keywords',batchids,'kw'),('new-all',batchids,'para'),('all-pattern',allpattern,'para')]:
   ii=[i for i,q in enumerate(qq) if q['target'] in ids and q['kind']==kind]
   if ii:derived[label]=from_arrays('new-rig',ii)
 save(BASE/'results/independent-rig-slices.json',derived)
 # Per-item regressions, all legs kept separate. No top-only or win-only reporting.
 regress=[]
 names=['sealed','sealed-ru','round1','rig']+(['new-rig'] if (BASE/'results/new-rig.json').exists() else [])+SETS
 for name in names:
  z=np.load(BASE/'results'/f'{name}.npz');qs=rows_for(name)
  for a in arms[2:]:
   for leg in ['vector','hybrid']:
    base=z['H.'+leg+'/rank'];new=z[a+'.'+leg+'/rank']
    for i in np.flatnonzero((base==1)&(new>3)):regress.append(dict(set=name,arm=a,leg=leg,query=qs[i]['id'],kind=qs[i].get('kind','public'),old_rank=1,new_rank=int(new[i]),text=qs[i]['text']))
 write(BASE/'results/rank1-to-below3.jsonl',regress)
 tables=['# Round 2 frozen tables','','MRR@10 / nDCG@10 / Recall@10 and paired differences are in results/*.json and per-query .npz. Intervals here are paired 95% bootstrap (10,000; seed 391; relevant-target connected clusters). Pointwise intervals; primary C fixed before tests. Hybrid is a single-field equal RRF(60), 30 hits per leg estimate.','']
 for name in names:
  data=load(name)
  for sl in (['para','kw','specific','ordinary','english','outside_round1','covered','uncovered','risk'] if name.startswith('sealed') else ['synthetic/para','public/para','synthetic/kw','synthetic/cross'] if name=='round1' else ['para','kw'] if 'rig' in name else ['all']):
   if sl not in data:continue
   r=data[sl];tables += [f'## {name} / {sl} (n={r["n"]}, clusters={r["clusters"]})','','| Arm | Vector MRR | BM25 MRR | Hybrid MRR | Δ vector vs H | Δ hybrid vs H | Δ hybrid vs Q |','|---|---:|---:|---:|---|---|---|']
   for a in arms:
    vals=[r['means'][a+'.'+leg]['mrr10'] for leg in ['vector','bm25','hybrid']];dv=r['differences'][a+'.vector']['H.vector']['mrr10'];dh=r['differences'][a+'.hybrid']['H.hybrid']['mrr10'];dq=r['differences'][a+'.hybrid']['Q.hybrid']['mrr10']
    tables.append(f'| {a} | '+ ' | '.join(f'{v:.4f}' for v in vals)+f' | {fmt(dv)} | {fmt(dh)} | {fmt(dq)} |')
   tables+=['']
 for label,r in derived.items():
  tables += [f'## Independent rig slice: {label} (n={r["n"]})','','| Arm | Vector MRR | BM25 MRR | Hybrid MRR | Δ vector vs H | Δ hybrid vs H |','|---|---:|---:|---:|---|---|']
  for a in arms:
   v=r['scores'][a+'.vector'];b=r['scores'][a+'.bm25'];h=r['scores'][a+'.hybrid'];tables.append(f'| {a} | {v["mrr"]:.4f} | {b["mrr"]:.4f} | {h["mrr"]:.4f} | {fmt(v["vs_head"])} | {fmt(h["vs_head"])} |')
  tables+=['']
 (BASE/'TABLES.md').write_text('\n'.join(tables)+'\n')
 d=lambda data,sl,a,leg,ref='H',metric='mrr10':data[sl]['differences'][a+'.'+leg][ref+'.'+leg][metric]
 m=lambda data,sl,a,leg,metric='mrr10':data[sl]['means'][a+'.'+leg][metric]
 primary={leg:d(sealed,'para','C',leg) for leg in ['vector','hybrid']};ind=derived.get('new-pattern',{}).get('scores',{})
 acceptance=dict(sealed_vector_positive=primary['vector']['lo']>0,sealed_hybrid_positive=primary['hybrid']['lo']>0,independent_vector_positive=ind.get('C.vector',{}).get('vs_head',{}).get('lo',-1)>0,independent_hybrid_positive=ind.get('C.hybrid',{}).get('vs_head',{}).get('lo',-1)>0,public={},controls={})
 for name in SETS:
  data=load(name);acceptance['public'][name]={leg:d(data,'all','C',leg,metric='ndcg10') for leg in ['vector','hybrid']}
 for sl in ['kw','specific','ordinary','english']:acceptance['controls'][sl]={leg:d(sealed,sl,'C',leg) for leg in ['vector','hybrid']}
 acceptance['passes']=all(acceptance[k] for k in ['sealed_vector_positive','sealed_hybrid_positive','independent_vector_positive','independent_hybrid_positive']) and all(v['lo']>=-.01 for x in acceptance['public'].values() for v in x.values()) and all(v['lo']>=-.02 for x in acceptance['controls'].values() for v in x.values())
 save(BASE/'results/acceptance.json',acceptance)
 out=['# Round 2: no tested cheap fix meets the acceptance bar','',
 '**Acceptance: FAILED. Recommendation: retain EG2-head and exact search. Do not ship the round-2 dictionary/lexical expansion or token alias patch.** This experiment tests a scalable public-source mapping and a no-training weight edit; it does not establish that every cheap fix is impossible.',
 '',f'The preregistered combined arm C changes sealed paraphrase MRR@10 by **{fmt(primary["vector"])} in vector search** and **{fmt(primary["hybrid"])} after fusion** (64 targets). Acceptance requires resolved gains in both, independent confirmation, and non-regression. `results/acceptance.json` records each gate.','',
 '## Independent confirmation','']
 if ind:
  out += [f'The **20 newly supplied, independently authored rig pattern cases** give C−H **{fmt(ind["C.vector"]["vs_head"])} vector** and **{fmt(ind["C.hybrid"]["vs_head"])} hybrid**. These texts were first opened after FROZEN.json. The 24 new cases (20 patterns + 4 controls) are reported separately from the original eight; the combined 24-pattern slice includes four original cases and is not 24 entirely new cases.','',
  '| Arm | Vector MRR | BM25 MRR | Hybrid MRR | Δ hybrid vs H |','|---|---:|---:|---:|---|']
  for a in arms:
   v=ind[a+'.vector'];b=ind[a+'.bm25'];h=ind[a+'.hybrid'];out.append(f'| {a} | {v["mrr"]:.3f} | {b["mrr"]:.3f} | {h["mrr"]:.3f} | {fmt(h["vs_head"])} |')
  out += ['',f'The external post reports EG2-head/Qwen MRR 0.476/0.825 in its private 1,458-document pool. Our same 20 targets give {ind["H.vector"]["mrr"]:.3f}/{ind["Q.vector"]["mrr"]:.3f} in the 832-document public pool. This difference in distractors limits transfer. No private chunk, private vector row or local service was accessed; external absolute ranks are provenance, not our reproduced measurements.','']
 out+=['## What was fixed in evaluation','',
 'The new 64-target set was authored and sealed before source acquisition, with 48/64 primary families outside round 1’s 40-entry glossary. It contains 64 topical competitors, three queries per target (plain Russian, keyword, specific), 12 ordinary Russian controls and 12 English controls, plus the existing 800 attributed public distractors: 952 documents, 216 queries. The primary paraphrase sample is 64 target clusters, not 192 independent observations.',
 '', 'The authored set has **temporal/source separation, not independent authorship**: this agent wrote the cases and executed the research. Unlike round 1, no dictionary definition came from an authored query; whole published categories supplied definitions mechanically. The fresh rig batch supplies the independent-author check. Known-item relevance is single-target, not exhaustive semantic judgment. Some English controls describe similar facts, so the prespecified `sealed-ru` sensitivity removes those 12 English documents/queries; it never replaces the primary.',
 '',f'On sealed-ru, C−H is {fmt(d(load("sealed-ru"),"para","C","vector"))} vector and {fmt(d(load("sealed-ru"),"para","C","hybrid"))} hybrid. No text/label or parameter was changed after the seal/freeze.',
 '', 'All differences use 10,000 paired bootstrap draws, seed 391. Queries sharing a relevant target are joined into connected components and resampled together; this also applies to public multi-relevance sets. Intervals are pointwise; acceptance requires all prespecified gates, rather than selecting the best arm. A nonsignificant loss is not proof of no regression. Material-loss margins were .01 nDCG on public sets and .02 MRR on controls.',
 '', '## Source mapping and honest coverage','',
 f'Whole Russian Wiktionary computer-slang/internet/game and programming categories supplied 1,320 revision snapshots. Extraction yielded **{len(G)} entries**, **{sum(bool(g["en"]) for g in G)} Latin-script etymon candidates**, and **{len(ACTIVE)} active plausible loanword mappings**. Published source text, permanent revisions, contributor-history links, extraction rules and CC BY-SA 4.0 notices are in `sources/`; no missing held-out/rig term was filled manually.',
 '',f'On the sealed set, exact family-name lookup finds {sum(r["exact_family_in_dictionary"] for r in coverage)}/64 families in the extracted dictionary and {sum(r["exact_family_active"] for r in coverage)}/64 in the active mapping. Actual vector expansion fires on {sum(r["vector_expanded"] for r in coverage)}/64 target messages. **A trigger may be incidental vocabulary, not successful coverage of the primary slang family.** `sealed-coverage-detail.jsonl` lists every matched lemma/meaning/source and every missed case; the covered/uncovered slices in TABLES use any document trigger and are labeled accordingly.',
 '', 'Exact-family lookup is conservative about derivatives and spelling variants (for example a family label and its verb may differ); raw entry count is not a claim of workspace coverage. Source senses include older desktop/internet language and can omit modern dev-team verbs. The lemma rules handle regular noun cases and common verb endings but do not perform general Russian morphological analysis. Fifty exact family labels are absent from the acquired category pages, one has no eligible extracted sense, five fail etymon/similarity activation, and eight are active before context gates. Improvements on an uncovered target can arise from changed competitors or lexical statistics; they do not establish acquisition of its missing slang meaning.',
 '', 'Active rows use an extracted etymon candidate and transliteration/source similarity ≥.45; source-language verification proved incomplete (see below). Short Cyrillic documents only (≤600 characters) can receive at most two definitions, each at most 18 words, with a 240-character suffix budget. Ambiguous dictionary senses require an independent technical cue. Untested senses/ambiguous flight `рейс` and wet `мок` are not filled by transliteration guesses. These conservative gates trade recall for precision and still do not resolve all sense ambiguity.',
 '', 'The lexical arm preserves original NFKC/casefold/UAX#29 tokens and adds missing canonical slang, English and definition tokens to both sides. Reverse definition matching excludes common definition words and needs at least two anchors and half the remaining content words (one for a true one-word meaning). Pure English inputs are unchanged by lexical expansion. Full Snowball stemming/ё folding exists only in S. This is source-based synonym expansion, not trained retrieval.',
 '', '## Arms and held-out retrieval','',
 '| Arm | Change |','|---|---|','| H | EG2-head, base BM25 |','| Q | Qwen3, base BM25 |','| V | document vector glossary, base BM25 |','| L | H vectors, bidirectional lexical glossary |','| C | V + L; preregistered primary |','| S | H vectors, Snowball lexical ablation |',f'| W{fr["weight_dose"]} | actual sparse token-row aliases, head, base BM25 |',f'| R{fr["weight_dose"]} | per-row norm/displacement matched random edits |','',
 '| Set / slice | H vector | Q vector | C vector | Δ C−H vector | H hybrid | C hybrid | Δ C−H hybrid |','|---|---:|---:|---:|---|---:|---:|---|']
 for name,sl in [('sealed','para'),('sealed','outside_round1'),('sealed','covered'),('sealed','uncovered'),('round1','synthetic/para'),('round1','public/para'),('rig','para')]:
  data=load(name)
  if sl not in data:continue
  out.append(f'| {name}/{sl} (n={data[sl]["n"]}) | {m(data,sl,"H","vector"):.3f} | {m(data,sl,"Q","vector"):.3f} | {m(data,sl,"C","vector"):.3f} | {fmt(d(data,sl,"C","vector"))} | {m(data,sl,"H","hybrid"):.3f} | {m(data,sl,"C","hybrid"):.3f} | {fmt(d(data,sl,"C","hybrid"))} |')
 out+=['','TABLES.md contains every arm’s vector, BM25 and fused ranks/metrics separately, including controls and external pattern slices. The full per-query arrays retain ranks/top-10 IDs and MRR/nDCG/Recall. No vector-to-hybrid contrast is presented as an intervention gain. Round-1 EG2/Qwen vector ranks and metrics reproduce exactly; three prior hybrid queries change under the preregistered stable document-ID tie order (round1-hybrid-tie-differences.json).','', '## Weight-space attempt','']
 spec=json.loads((CODE/'alias_spec.json').read_text());dev=json.loads((BASE/'dev/dev.json').read_text());out += [f'Edited **{len(spec)} actual token_embd rows** from independently sourced unambiguous loanword forms. English source token rows are averaged and norm-matched, then interpolated at .25/.5/1 and renormalized to the original row norm. Random edits preserve each final norm and match each row’s displacement. WEIGHT_TABLES.md reports all dev doses against their random controls, including ordinary and English retrieval. `alias_norms.json` records numerical checks; `alias_rows.npz` is the compact reproducible patch.',
 '', 'The faithful graph computes both the residual input and the per-layer embedding (PLE) projection from the same `token_embd` lookup. Therefore the sparse edit changes both paths consistently; no separate PLE lookup table is left stale. Final audit measures the projected PLE difference. This is an inference-time raw weight edit, not a post-hoc vector transform, tokenizer text replacement or training step. No requantized alias GGUF was produced; these negative tests use edited dequantized Q8 rows in the validated MLX graph, not a new native deployment artifact.',
 '', '| Dose | Dev slang Δ vector | Dev slang Δ hybrid | Ordinary Δ vector | Ordinary Δ hybrid |','|---|---|---|---|']
 for dose in [.25,.5,1.]:
  a='W'+str(dose);out.append(f'| {dose} | {fmt(d(dev,"synthetic/para",a,"vector"))} | {fmt(d(dev,"synthetic/para",a,"hybrid"))} | {fmt(d(dev,"ordinary",a,"vector"))} | {fmt(d(dev,"ordinary",a,"hybrid"))} |')
 out += ['',f'No positive eligible dev dose existed. W{fr["weight_dose"]} is frozen as a **rejected diagnostic**, not a selected improvement. The higher dose exposes collateral ordinary-Russian damage. Whole-word tokens such as inflections of `ник` and `комп` can still occur inside ordinary words; dedicated probes and public sets test that sharing.',
 '', 'The headline slang often splits into generic pieces: хотфикс → ▁хо / т / фи / кс; хендлер → ▁х / ен / д / лер; задеплоить → ▁за / де / пло / ить; ревью → ▁ре / вью. Changing those common pieces globally would not implement a selective alias. The current constrained rows do not cover these key bridges. This rejects this cheap alias design, not all possible multilingual weight editing.',
 '', '## Non-regression and dilution risks','', 'The combined arm also fails the preregistered public margin on RU wiki: vector nDCG falls 0.0131 (CI −0.0394 to 0), and hybrid falls 0.0081 (CI −0.0242 to 0). Thus aggregate public safety is not established even though FiQA, SciFact and German MIRACL are unchanged. Specificity controls also lose 0.036 vector MRR, exceeding their .02 material-loss margin.', '', 'Independent s20 is a useful success: a source definition of линтер moves vector rank 52→4 and fused rank 6→1. But s15 goes vector 1→4 after a generic коммит definition dilutes the specific forced-push/rollback intent. s12 has no document expansion yet goes fused 2→10 under C as reverse lexical expansion adds a noisy table definition. In the original eight-case pool, s08 stays vector rank 1, while s05 deteriorates 54→372 under V. All cases, including these losses, remain in the aggregate. See failure-case-expansions.json and RIG_RANKS.md.', '', '| Public set | Queries | Δ C vector nDCG | Δ C hybrid nDCG | Δ W vector nDCG | Δ W hybrid nDCG |','|---|---:|---|---|---|---|']
 for name in SETS:
  data=load(name);w='W'+str(float(fr['weight_dose']));out.append(f'| {name} | {data["all"]["n"]} | {fmt(d(data,"all","C","vector",metric="ndcg10"),4)} | {fmt(d(data,"all","C","hybrid",metric="ndcg10"),4)} | {fmt(d(data,"all",w,"vector",metric="ndcg10"),4)} | {fmt(d(data,"all",w,"hybrid",metric="ndcg10"),4)} |')
 out += ['', 'FiQA uses all 57,638 documents and 648 test queries. SciFact uses all 5,183 documents and 300 test queries. MIRACL RU/DE remain the prior 200-query **closed pools**, not full MIRACL retrieval: passage IDs in the repack cannot be independently recovered. Wiki stress and RU wiki are the prior public sets. Existing models/banks are reused only via the reconstructed public prefix and verified by fresh EG2/Qwen encodings.',
 '', '| Sealed controls | Δ C vector MRR | Δ C hybrid MRR |','|---|---|---|']
 for sl in ['kw','specific','ordinary','english','risk']:out.append(f'| {sl} | {fmt(d(sealed,sl,"C","vector"))} | {fmt(d(sealed,sl,"C","hybrid"))} |')
 out += ['',f'`results/rank1-to-below3.jsonl` records all {len(regress)} arm/leg/query rank-1→>3 events, including duplicate observations across sensitivity pools; these are not {len(regress)} independent failures. `RIG_RANKS.md` records every external case in the current public pool. Even a scoped glossary can dilute specific intent or shift BM25 length/IDF and RRF ordering. Exact no-op inputs and unchanged metrics are distinguished by per-set coverage counts.','',
 '## Exact recommendation and reproducible rig protocol','',
 'Keep the existing production EG2 Q8 + exact head projection. Do not alter tokenizer rows, enable this glossary, or deploy a stemmer on the evidence here. The delivered scripts/data are an auditable rejected-candidate experiment, not a production patch. No retraining or model download is required to reproduce it.',
 '', 'If the teammate runs the frozen candidate for diagnostic replication:',
 '', '1. Hash the original workspace pool/query/qrels export and all round-2 frozen files before scoring. Keep the independent 20 new patterns and four controls separate from the old eight and from the original 30 Russian queries. Never add uncovered terms after seeing outcomes.',
 '2. Fix corpus/chunks/IDs, context 2048, EG2 production prefixes, mean pooling, head projection and exact search. Keep Qwen’s production query instruction and last-token pooling. Do not apply the head twice. Use the original text for display/citations.',
 '3. Run in fixed order H, Q, V, L, C, S, W0.25, R0.25, one heavy process at a time. V/C use `pipeline2.vector_text`; L/C use `expanded_tokens` on docs and queries; S uses `stem_tokens`. W/R load the sparse rows via `alias2.apply` in the faithful MLX model. Verify model hashes first. Re-embed/reindex each changed document; never mix incompatible banks.',
 '4. First use the estimator settings exactly: BM25 k1=1.2, b=.75, original-token OR semantics, positive lexical hits, equal RRF k=60, 30 hits/leg. Record the production-index result separately if titles, adaptive weights, filters or operators are enabled. A proxy hybrid gain does not certify production fusion.',
 '5. Save each query’s target rank in vector, BM25 and fusion, top-10 IDs/scores, coverage/matched source entries, truncation and elapsed time. Report MRR/nDCG/Recall@10, paired target-cluster 95% bootstrap CIs, keyword/paraphrase/control/language slices, and every rank-1→>3 loss. Do not discard uncovered terms or suppress losses.',
 '6. Promotion still requires positive lower CI bounds for the preregistered primary vector and hybrid deltas on genuinely independent queries, and non-inferiority at the frozen .01/.02 margins. Keep the original 30-query workspace Russian result as its own endpoint. Failure here is not repaired by choosing the best retrospective arm.',
 '', '## Provenance and limits','',
 'SEALED.json preceded source acquisition; FROZEN.json preceded all held-out scoring and opening raw rig batches. One dev-only source/rule revision is documented in AMENDMENTS.md with the first results preserved. A prescore verb-inflection/MLX-indexing repair was also preserved. No held-out tuning occurred. All result tables derive from saved ranks; reporting never re-encodes or reranks.',
 '', 'The fresh rig data makes independent testing possible, but these synthetic, single-target pools do not reproduce the private workspace. A post-score source audit found 35/337 etymon candidates came from unresolved generic templates (33 active), including the false English assignment таблица → tabula. The extractor and scores are preserved unchanged; SOURCE_LIMITATIONS.md explains why this defect further prevents a production recommendation or a universal claim that no cheap fix exists. Dictionary senses/English etymons are automatically extracted and not exhaustively manually vetted. Conservative gates leave many phrases unchanged; reverse lexical matching can still be semantically noisy. The non-regression battery does not cover every ordinary Russian word sharing a subtoken.',
 '', 'All writes stay in the two round-2 directories; no git state changes, account reads or local ports. AUDIT.json verifies round-1/model/frozen hashes, cache parity and the <5 GB limit. The artifact is research-only and carries source attribution in sources/ATTRIBUTION.md.']
 (BASE/'REPORT.md').write_text('\n'.join(out)+'\n')
 # Rig ranks, coverage, external metadata, and full per-leg audit.
 lines=['# Frozen rig ranks in public distractor pools','','External rig ranks use private distractors and are not directly comparable. All new rows were opened after freeze.','']
 for name,raw in [('rig',read(OLD/'from-rig/synthetic-lookalikes.jsonl'))]+([('new-rig',read(BASE/'from-rig/lookalikes-batch1.jsonl')+batch2)] if batch2 else []):
  qs=rows_for(name);z=np.load(BASE/'results'/f'{name}.npz');lines += [f'## {name}','','| ID | External H/Q | H vec/BM25/fused | Q vec/BM25/fused | C vec/BM25/fused | V vec/BM25/fused | Matched lemmas |','|---|---|---|---|---|---|---|']
  for r in raw:
   i=next(i for i,q in enumerate(qs) if q['target']==r['id'] and q['kind']=='para');ranks=lambda a:'/'.join(str(int(z[a+'.'+leg+'/rank'][i])) for leg in ['vector','bm25','hybrid']);matches=', '.join(G[j]['lemma'] for j in doc_matches(r['text'])) or 'none';ext=r['rig_para_rank'];lines.append(f'| {r["id"]} | {ext["eg2_head"]}/{ext["qwen3"]} | {ranks("H")} | {ranks("Q")} | {ranks("C")} | {ranks("V")} | {matches} |')
 (BASE/'RIG_RANKS.md').write_text('\n'.join(lines)+'\n')
 log('Rendered report, all-arm tables, source-labelled independent rig slices, acceptance gates and rank-regression ledger from saved scores only.')
if __name__=='__main__':main()
