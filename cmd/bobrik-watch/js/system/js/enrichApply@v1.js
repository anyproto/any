// __main_source
// enrichApply@v1 — DETERMINISTIC apply of an enrich_proposal. Thin wrapper over
// the server endpoint POST /v1/spaces/:space/enrich/apply, which is the single
// apply implementation shared by this tool and the any-ui "Apply" button. The
// server: for each enrich_proposal_items record creates the target object for
// `new` items (items sharing newType+newName map to ONE object; grouped facts
// land together, each keeping its own source), sets the real property for
// `property` items, always writes an enriched_data record onto the target, then
// deletes the ephemeral proposal. No LLM. Idempotent (a deleted proposal reads
// no items -> 404).

// apply — TOOL entry. Applies the reviewed proposal and deletes it.
export function apply(proposalId, opts) {
  opts = opts || {};
  var space = opts.space || (typeof env !== "undefined" && env.ANY_SPACE_ID);
  var apiBaseUrl = opts.apiBaseUrl || (typeof env !== "undefined" && env.ANY_API_URL);
  if (!proposalId) return { ok: false, error: "proposalId required" };
  if (!space) return { ok: false, error: "opts.space required (the space holding the proposal + targets)" };

  var r = fetch(apiBaseUrl + "/v1/spaces/" + space + "/enrich/apply", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ proposalId: proposalId }),
  });
  if (!r || !r.ok) {
    var detail = r && r.body ? JSON.stringify(r.body) : "";
    return { ok: false, error: "apply failed: HTTP " + (r && r.status) + " " + detail };
  }
  var b = r.body || {};
  return {
    ok: true,
    proposalId: proposalId,
    created: b.created,
    propertiesSet: b.propertiesSet,
    enrichedDataWritten: b.enrichedDataWritten,
    proposalDeleted: b.proposalDeleted,
    failures: b.failures || [],
  };
}
