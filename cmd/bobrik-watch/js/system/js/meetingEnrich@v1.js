// __main_source
// meetingEnrich@v1 — meeting transcript → a structured, sourced enrichment
// PROPOSAL (stage 1 of meeting enrichment; see the dev-space plan + the
// meeting-enrich skill). It does the mechanical, token-heavy work so it never
// floods the agent's context:
//   1. read the transcript's editor_blocks (each line prefixed with its stable
//      block id) and SYNTHESIZE knowledge units — each cites the source
//      block ids it was derived from (multi-block, scattered, implied; final
//      state only).
//   2. ground each unit against the target space on its clean English statement
//      (cheap semsearch; candidate names resolved so matches are recognizable).
//   3. raw reconcile → new|enrich|conflict|redundant.
//   4. PERSIST a draft `enrich_proposal` object whose `enrich_proposal_items`
//      dataset holds one record per item (text + source + target). Return a
//      compact handle.
// Stage 2 (consolidate: cluster/dedup/set targets+values by EDITING the
// proposal's items) and stage 3 (apply via enrichApply, which also deletes the
// proposal) are NOT here — they are the agent's job + the deterministic apply
// program. Nothing on target objects is written here.
//
// Item record shape written into enrich_proposal_items (see internal/enrichproposal):
//   { text, source, outcome:"enrich|new",
//     targetObjectId, targetKind:"collection|property", targetProperty, value,
//     newType, newName }
//
// args / opts: { space (target, required for the tool), transcriptId | transcript,
//                apiBaseUrl?, limit? }

import { search as semsearch } from "semsearch@v1";
import { chatUntraced } from "llm@v1";

var EXTRACT_SYS =
  "You read a meeting transcript and SYNTHESIZE its knowledge into discrete units. " +
  "A unit is a decision, a fact about an entity, an action item, or a relation. " +
  "A unit may be derived from scattered/non-contiguous lines, or implied (proposed then agreed). " +
  "If something was revised during the meeting, capture only the FINAL state. " +
  "Each transcript line is prefixed with its block id in brackets, e.g. `[Tf1csw3SDTC] text`. " +
  "Return STRICT JSON: an array of " +
  '{ "id":"u1", "kind":"decision|fact|task|relation", "statement":"...", ' +
  '"entities":["Project Phoenix","billing module"], "sourceBlocks":["Tf1csw3SDTC","Tf1csw3SDTC:4"], "support":["short verbatim quote"] }. ' +
  "sourceBlocks = the block ids (copied EXACTLY from the [brackets]) this unit is derived from (1 or more, may be non-contiguous). " +
  "entities = named CONCEPTS / projects / docs / features this unit is about — NOT individual people or speakers. " +
  "Write statement and entities in ENGLISH even if the transcript is in another language (they search an English-leaning knowledge base). " +
  "support = 1-3 short verbatim excerpts (keep original language). No prose outside the JSON.";

var RECONCILE_SYS =
  "You reconcile synthesized meeting knowledge against a space's existing objects. " +
  "For EACH knowledge unit decide one outcome:\n" +
  "  new       — no matching object exists; mint one.\n" +
  "  enrich    — a matching object exists; add a fact or set a property.\n" +
  "  conflict  — a matching object exists but the transcript contradicts it.\n" +
  "  redundant — already fully known; drop.\n" +
  "Use ONLY the provided candidate objects as possible matches; never invent ids. " +
  "When uncertain whether a candidate truly matches, prefer `new` (a duplicate is " +
  "safer to review than a wrong merge).\n" +
  "Return STRICT JSON: { \"actions\": [ {\n" +
  '  "unitId":"u1", "outcome":"new|enrich|conflict|redundant",\n' +
  '  "targetObjectId":"<existing id or null>",\n' +
  '  "newType":"<xKey for new, else null>", "newName":"<title for new, else null>",\n' +
  '  "update":{ "kind":"propUpdate|bodyUpdate|null", "field":"<prop name or null>", "value":"<text>" },\n' +
  '  "reason":"one line"\n' +
  "} ] }\nNo prose outside the JSON.";

function _text(resp) {
  if (!resp || !resp.content) return "";
  var out = "";
  for (var i = 0; i < resp.content.length; i++) {
    if (resp.content[i].type === "text") out += resp.content[i].text;
  }
  return out;
}

function _llm(prompt, maxTokens) {
  var resp = chatUntraced([{ role: "user", content: prompt }], { tier: "codegen", max_tokens: maxTokens || 16000 });
  return _text(resp);
}

// tolerant JSON parse: strip fences, skip leading prose to first [ or {, repair
// invalid JSON escapes (markdown-escaped underscores from verbatim transcript
// text produce "\_", which is not legal JSON and crashes JSON.parse).
function _parse(txt) {
  if (!txt) return null;
  var s = String(txt).trim();
  var fence = s.match(/```(?:json)?\s*([\s\S]*?)```/);
  if (fence) s = fence[1].trim();
  var start = s.search(/[\[{]/);
  if (start > 0) s = s.slice(start);
  try { return JSON.parse(s); } catch (e) {
    try { return JSON.parse(s.replace(/\\(?!["\\/bfnrtu])/g, "")); } catch (e2) { return null; }
  }
}

function _post(url, body) {
  return fetch(url, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
}

// Batch-resolve object display names by id so reconcile recognizes candidates
// by title (a content-hit candidate carries no name otherwise).
function _resolveNames(apiBaseUrl, spaceId, ids) {
  var out = {};
  if (!ids.length) return out;
  var r = _post(apiBaseUrl + "/v1/spaces/" + spaceId + "/objects/query", { filter: { id: { $in: ids } }, limit: ids.length });
  if (r && r.ok && r.body && r.body.records) {
    for (var i = 0; i < r.body.records.length; i++) {
      var rec = r.body.records[i];
      var nm = rec.any && rec.any.name;
      if (rec.id && nm) out[rec.id] = nm;
    }
  }
  return out;
}

// Read the transcript's editor_blocks in document order; return block-cited
// text ("[blockId] text" per line) + the set of valid block ids.
function _loadBlocks(apiBaseUrl, spaceId, transcriptId) {
  var r = _post(apiBaseUrl + "/v1/spaces/" + spaceId + "/query", { objectId: transcriptId, dataset: "editor_blocks", sort: ["nav.pos"], limit: 5000 });
  if (!r || !r.ok) return null;
  var recs = (r.body && r.body.records) || [];
  var lines = [], ids = {};
  for (var i = 0; i < recs.length; i++) {
    var b = recs[i];
    if (!b.id) continue;
    ids[b.id] = true;
    var t = (b.text || "").trim();
    if (t) lines.push("[" + b.id + "] " + t);
  }
  return { citedText: lines.join("\n"), validIds: ids };
}

function _buildSource(spaceId, transcriptId, blocks) {
  if (!transcriptId) return "";
  var base = "any://" + spaceId + "/" + transcriptId;
  if (blocks && blocks.length) return base + "#" + blocks.join(",");
  return base;
}

// runEnrich — the analysis core (no persistence). Returns the raw report.
function runEnrich(args) {
  args = args || {};
  var spaceId = args.spaceId;
  var apiBaseUrl = args.apiBaseUrl || (typeof env !== "undefined" && env.ANY_API_URL);
  var limit = args.limit || 6;
  if (!spaceId) return { ok: false, error: "spaceId required (the target space to reconcile against)" };
  if (!apiBaseUrl) return { ok: false, error: "apiBaseUrl required (or env.ANY_API_URL)" };

  // 1. load transcript — blocks (cited) when transcriptId; else raw text.
  var transcript, validIds = null;
  if (args.transcriptId) {
    var lb = _loadBlocks(apiBaseUrl, spaceId, args.transcriptId);
    if (!lb) return { ok: false, error: "read transcript blocks " + args.transcriptId + " failed" };
    transcript = lb.citedText;
    validIds = lb.validIds;
  } else if (args.transcript) {
    transcript = args.transcript;
  }
  if (!transcript) return { ok: false, error: "transcript or transcriptId required" };

  // 2. extract knowledge units (synthesis, multi-block, block-cited). Retry once.
  var extractPrompt = EXTRACT_SYS + "\n\nTRANSCRIPT:\n\n" + transcript;
  var units = null, rawExtract = "";
  for (var attempt = 0; attempt < 2 && (!units || !units.length); attempt++) {
    rawExtract = _llm(extractPrompt, 32000);
    units = _parse(rawExtract);
  }
  if (!units || !units.length) {
    return { ok: false, error: "no knowledge units extracted", transcriptChars: transcript.length, rawExtractHead: String(rawExtract).slice(0, 600) };
  }
  // keep only block ids that really exist (drop hallucinated citations).
  for (var ui = 0; ui < units.length; ui++) {
    var sb = units[ui].sourceBlocks || [];
    if (validIds) sb = sb.filter(function (id) { return validIds[id]; });
    units[ui].sourceBlocks = sb;
  }

  // 3. ground each unit on its statement (cheap semsearch); resolve names.
  var sourceId = args.transcriptId || null;
  var grounded = {};
  var allCandIds = {};
  for (var i = 0; i < units.length; i++) {
    var u = units[i];
    var probe = u.statement || (u.entities || []).join(" ");
    if (!probe) { grounded[u.id] = []; continue; }
    var cands = {};
    var sr = semsearch(probe, { space: spaceId, limit: limit });
    if (sr && sr.ok && sr.hits) {
      for (var h = 0; h < sr.hits.length; h++) {
        var hit = sr.hits[h];
        if (sourceId && hit.objectId === sourceId) continue;
        if (!cands[hit.objectId]) { cands[hit.objectId] = { objectId: hit.objectId, name: null, snippets: [] }; allCandIds[hit.objectId] = true; }
        if (hit.dataset === "prop" && hit.recordId === "name") cands[hit.objectId].name = hit.data;
        if (hit.data) cands[hit.objectId].snippets.push(String(hit.data).slice(0, 160));
      }
    }
    grounded[u.id] = Object.keys(cands).map(function (k) { return cands[k]; });
  }
  var nameById = _resolveNames(apiBaseUrl, spaceId, Object.keys(allCandIds));
  for (var uid in grounded) {
    if (!Object.prototype.hasOwnProperty.call(grounded, uid)) continue;
    var arr = grounded[uid];
    for (var c = 0; c < arr.length; c++) {
      if (!arr[c].name && nameById[arr[c].objectId]) arr[c].name = nameById[arr[c].objectId];
    }
  }

  // 4. type catalog (so `new` actions name a real type)
  var types = [];
  var tr = fetch(apiBaseUrl + "/v1/spaces/" + spaceId + "/types");
  if (tr && tr.ok && tr.body && tr.body.types) {
    types = tr.body.types
      .filter(function (t) { return !t.builtIn || t.xKey === "editor"; })
      .map(function (t) { return t.xKey + " — " + t.name + ": " + (t.description || ""); });
  }

  // 5. reconcile -> action map
  var user =
    "AVAILABLE TYPES (xKey — name):\n" + types.join("\n") +
    "\n\nKNOWLEDGE UNITS:\n" + JSON.stringify(units) +
    "\n\nGROUNDED CANDIDATES PER UNIT (keyed by unitId; existing objects — match a unit against ITS OWN candidates):\n" + JSON.stringify(grounded);
  var recon = _parse(_llm(RECONCILE_SYS + "\n\n" + user, 24000)) || {};
  var actions = recon.actions || [];

  var tally = {};
  for (var a = 0; a < actions.length; a++) {
    var o = actions[a].outcome || "?";
    tally[o] = (tally[o] || 0) + 1;
  }

  return { ok: true, space: spaceId, transcriptId: sourceId, units: units.length, tally: tally, units_detail: units, grounded: grounded, actions: actions };
}

// propose — TOOL entry. Runs the analysis and persists a DRAFT enrich_proposal
// object (one enrich_proposal_items record per item). Returns a compact handle;
// the agent then consolidates the items per the meeting-enrich skill.
export function propose(transcriptId, opts) {
  opts = opts || {};
  var spaceId = opts.space || (typeof env !== "undefined" && env.ANY_SPACE_ID);
  var apiBaseUrl = opts.apiBaseUrl || (typeof env !== "undefined" && env.ANY_API_URL);
  if (!transcriptId) return { ok: false, error: "transcriptId required" };
  if (!spaceId) return { ok: false, error: "opts.space required (the target space holding the transcript)" };

  var report = runEnrich({ transcriptId: transcriptId, spaceId: spaceId, apiBaseUrl: apiBaseUrl, limit: opts.limit });
  if (!report.ok) return report;

  var tname = "transcript";
  var nm = _resolveNames(apiBaseUrl, spaceId, [transcriptId]);
  if (nm[transcriptId]) tname = nm[transcriptId];
  var propName = "Enrichment proposal — " + tname;

  var cr = _post(apiBaseUrl + "/v1/spaces/" + spaceId + "/objects", { types: ["enrich_proposal"], initialProperties: { any: { name: propName } } });
  if (!cr || !cr.ok || !cr.body || !cr.body.objectId) {
    return { ok: false, error: "create proposal object failed: HTTP " + (cr && cr.status), report: report };
  }
  var proposalId = cr.body.objectId;

  var unitsById = {};
  for (var i = 0; i < report.units_detail.length; i++) unitsById[report.units_detail[i].id] = report.units_detail[i];

  var written = 0, tally = {};
  for (var a = 0; a < report.actions.length; a++) {
    var act = report.actions[a];
    var outcome = act.outcome || "new";
    if (outcome === "redundant") { tally.redundant = (tally.redundant || 0) + 1; continue; }
    var unit = unitsById[act.unitId] || {};
    var up = act.update || {};
    var isProp = up.kind === "propUpdate";
    var item = {
      text: unit.statement || up.value || "",
      source: _buildSource(spaceId, transcriptId, unit.sourceBlocks),
      outcome: outcome === "conflict" ? "enrich" : outcome,
      targetObjectId: act.targetObjectId || "",
      targetKind: isProp ? "property" : "collection",
      targetProperty: isProp ? (up.field || "") : "",
      value: isProp ? (up.value || "") : "",
      newType: act.newType || "",
      newName: act.newName || "",
    };
    var ops = [];
    for (var k in item) {
      if (Object.prototype.hasOwnProperty.call(item, k) && item[k] !== "") ops.push({ type: "$set", path: k, value: item[k] });
    }
    if (!ops.length) continue;
    var wr = _post(apiBaseUrl + "/v1/spaces/" + spaceId + "/modify", { objectId: proposalId, dataset: "enrich_proposal_items", records: [{ id: "", upsert: true, ops: ops }] });
    if (wr && wr.ok) { written++; tally[item.outcome] = (tally[item.outcome] || 0) + 1; }
  }

  // brief human-readable body (the items dataset is the source of truth).
  var body = "# " + propName + "\n\n" + written + " proposed enrichment items in the `enrich_proposal_items` dataset. Review/edit the items, then apply with `enrichApply`.\n\nSource: " + _buildSource(spaceId, transcriptId, null) + "\n";
  fetch(apiBaseUrl + "/v1/spaces/" + spaceId + "/objects/" + proposalId + "/editor/markdown", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ content: body }) });

  return { ok: true, proposalId: proposalId, proposalLink: "any://" + spaceId + "/" + proposalId, space: spaceId, transcriptId: transcriptId, items: written, tally: tally };
}

export function main(args) {
  return runEnrich(args);
}
