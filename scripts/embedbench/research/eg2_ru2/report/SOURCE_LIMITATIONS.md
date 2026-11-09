# Post-score source audit — no intervention changed

The frozen extractor's English-etymon fallback accepts a Latin-script name in
`{{этимология:NAME|...}}` without resolving the template's language. This is too
permissive. Of 337 candidate etymons, 302 have an explicit English marker in the
stored etymology excerpt; 35 are unresolved generic references, 33 of them active.
An explicit marker is not exhaustive manual sense validation either.

One concrete false English assignment is `таблица → tabula`, retained from
Wiktionary revision 14399793. The underlying template describes Latin. In the
independent s12 query, this noisy expansion adds `tabula` and an irrelevant generic
table definition. The lexical/fused result regresses (H rank 2 → C 10); this is
consistent with noisy expansion, although no post-hoc isolated-word ablation was run.
This is an implementation/source-validation weakness, not evidence that a properly
validated glossary must fail. `results/etymology-audit.jsonl` preserves all rows.

No dictionary, row edit, score or frozen script was changed after this finding.
The reported numbers are for the exact preregistered/frozen prototype. A corrected
strict-English source extractor would be a new candidate needing fresh independent
confirmation; these already-consumed tests cannot certify it. The research verdict
is **no tested candidate is ready to ship**, not a proof that no cheap fix exists.
