// __main_source
// enrichApply@v1 — DETERMINISTIC apply of an enrich_proposal. No LLM, no
// judgment: it just executes the reviewed items and deletes the proposal.
// Stage 3 of meeting enrichment (stage 1 = meetingEnrich.propose, stage 2 =
// bao consolidating the items per the meeting-enrich skill).
//
// For each enrich_proposal_items record:
//   - outcome "new" with no targetObjectId → createObject(newType, {name:newName})
//   - targetKind "property" → set the REAL property on the target
//     (updateObject with {<typeXKey>:{<propXKey>: value}})
//   - always → write an enriched_data record onto the target {text, source,
//     target, value} (the durable, sourced collection; for property items this
//     records that the property's value came from `source`)
// then deleteObject(proposalId) — proposals are ephemeral.
//
// Idempotent: the proposal is deleted on success, so a second apply reads no
// items and no-ops.

import { createClient } from "anyHelper@v1";

function _client() {
  return createClient({
    apiBaseUrl: (typeof env !== "undefined" && env.ANYTYPE_API_URL) || undefined,
    apiKey: (typeof env !== "undefined" && env.ANYTYPE_API_KEY) || undefined,
    spaceId: (typeof env !== "undefined" && env.ANYTYPE_SPACE_ID) || undefined,
    noTrace: true,
  });
}

// _writeEnriched posts one enriched_data record via the bespoke endpoint (it
// attaches the enriched_data type to the target object on first write).
function _writeEnriched(apiBaseUrl, space, objectId, text, source, target, value) {
  var body = { text: text };
  if (source) body.source = source;
  if (target) body.target = target;
  if (value) body.value = value;
  var r = fetch(apiBaseUrl + "/v1/spaces/" + space + "/objects/" + objectId + "/enriched-data", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  return !!(r && r.ok);
}

// apply — TOOL entry. Applies the reviewed proposal and deletes it.
export function apply(proposalId, opts) {
  opts = opts || {};
  var space = opts.space || (typeof env !== "undefined" && env.ANY_SPACE_ID);
  var apiBaseUrl = opts.apiBaseUrl || (typeof env !== "undefined" && env.ANY_API_URL);
  if (!proposalId) return { ok: false, error: "proposalId required" };
  if (!space) return { ok: false, error: "opts.space required (the space holding the proposal + targets)" };

  var client = _client();
  var items;
  try {
    items = client.getObjects({ objectId: proposalId, dataset: "enrich_proposal_items", space: space });
  } catch (e) {
    return { ok: false, error: "read proposal items: " + ((e && e.message) || e) };
  }
  if (!items || !items.length) {
    return { ok: false, error: "no items in proposal " + proposalId + " (already applied, deleted, or empty)" };
  }

  var created = 0, propertiesSet = 0, enriched = 0, failures = [];
  for (var i = 0; i < items.length; i++) {
    var it = items[i];
    var targetId = it.targetObjectId || "";

    // 1. create the target object for `new` items.
    if (it.outcome === "new" && !targetId) {
      if (!it.newType || !it.newName) { failures.push("new item missing newType/newName: " + (it.text || "").slice(0, 50)); continue; }
      var cr = client.createObject(it.newType, { name: it.newName, space: space });
      if (!cr || !cr.ok || !cr.id) { failures.push("create '" + it.newName + "' failed: " + (cr && cr.error)); continue; }
      targetId = cr.id;
      created++;
    }
    if (!targetId) { failures.push("no target for item: " + (it.text || "").slice(0, 50)); continue; }

    // 2. property items: set the REAL property value on the target.
    var recTarget = "", recValue = "";
    if (it.targetKind === "property" && it.targetProperty && it.value) {
      var dot = it.targetProperty.indexOf(".");
      if (dot > 0) {
        var typeKey = it.targetProperty.slice(0, dot);
        var propKey = it.targetProperty.slice(dot + 1);
        var data = { space: space };
        data[typeKey] = {};
        data[typeKey][propKey] = it.value;
        var ur = client.updateObject(targetId, data);
        if (ur && ur.ok) { propertiesSet++; recTarget = it.targetProperty; recValue = it.value; }
        else { failures.push("set " + it.targetProperty + " on " + targetId + " failed: " + (ur && ur.error)); }
      } else {
        failures.push("bad targetProperty (need type.prop): " + it.targetProperty);
      }
    }

    // 3. durable provenance / collection record (always).
    var text = it.text || it.value || "(enrichment)";
    if (_writeEnriched(apiBaseUrl, space, targetId, text, it.source || "", recTarget, recValue)) enriched++;
    else failures.push("enriched_data write failed on " + targetId);
  }

  // 4. delete the ephemeral proposal.
  var del = client.deleteObject(proposalId, { space: space });

  return {
    ok: true,
    proposalId: proposalId,
    created: created,
    propertiesSet: propertiesSet,
    enrichedDataWritten: enriched,
    proposalDeleted: !!(del && del.ok),
    failures: failures,
  };
}
