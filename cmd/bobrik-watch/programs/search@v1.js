// __main_source
// search@v1 — RLM-style search over the space's datasets. Design:
// docs/12-rlm-search.md (read it before changing loop mechanics).
//
// The corpus (agent_memory_items / agent_chunks+agent_turns / space objects)
// is treated as ENVIRONMENT, not prompt: an isolated root LLM loop — its own
// messages[], invisible to the calling agent's context — writes JS cells that
// page the corpus through indexed reads and map cheap batched sub-LLM calls
// (llm.completeBatchDetailed, classify tier) over snippets. Only the distilled
// {results, stats} return to the caller.
//
// Cell execution: `new Function("rlm","s","anyHelper","convmemory","llm","query", code)`
// — NOT js.eval. Function scope contains the root's `var`s (nothing leaks to
// the shared kernel; verified semantics), `s` is the root's cross-cell
// scratch, and there is no global namespace to clean up. This supersedes the
// design doc's IIFE-over-js.eval sketch (§5/§9.1): no nested js.eval, no
// globalThis.__rlm. Remaining hazard (documented in the root prompt): a bare
// `x = 1` assignment DOES create a kernel global — cells must use `var`.
//
// Guardrails are caps-with-wrapup, never budgets, never lossy truncation:
// on MAX_TOOLCALLS / MAX_ROOT_TURNS / context-ceiling the root gets one
// wrap-up turn to emit rlm.final with best-so-far; large cell returns stay
// lossless in scratch (s._N) with a schema preview inline; coverage is
// reported in stats, not silently capped.

import { createClient } from "anyHelper@v1";
import { createConvMemory } from "convmemory@v1";
import { createLLM, parseJSON, completeBatchDetailed } from "llm@v1";
import { displayValue, inferSchema } from "utils@v1";

// ============================================================================
// CONSTANTS
// ============================================================================

var MAX_TOOLCALLS = 12;        // root cells before forced wrap-up
var MAX_ROOT_TURNS = 8;        // root LLM turns before forced wrap-up
var HARD_TURN_STOP = 12;       // absolute turn ceiling (wrap-up + nudge headroom)
var CONTEXT_CEILING_CHARS = 320000; // ~80k tokens of root context → wrap-up
var INLINE_VALUE_CHARS = 4000; // bigger cell returns: schema preview, value stays in s._N
var CLASSIFY_BATCH = 20;       // snippets per classify prompt
var DEFAULT_K = 8;

// ============================================================================
// Root tool — single cell tool, function-body semantics
// ============================================================================

var CELL_TOOL = {
  name: "run_cell",
  description: "Execute a JavaScript FUNCTION BODY. Parameters in scope: " +
    "`rlm` (search primitives), `s` (scratch object that persists across your cells — " +
    "stash anything you need later), `anyHelper` (paged reads: getObjects/getObject/" +
    "describeType/getTypes), `convmemory` (indexed memory/history reads), `llm` " +
    "(sub-LLM: classify/chat — prefer rlm.mapClassify for bulk), `query` (the search query string). " +
    "Use `return <value>` to surface a value — there is NO implicit last-expression result. " +
    "ALWAYS declare with `var`; a bare `x = 1` leaks into the shared kernel. " +
    "Do NOT use import. Finish the whole search by calling rlm.final({...}) in a cell.",
  input_schema: {
    type: "object",
    properties: {
      code: {
        type: "string",
        description: "JavaScript function body. `return` surfaces a value; rlm.final({results: [...]}) ends the search."
      }
    },
    required: ["code"]
  }
};

// ============================================================================
// Snippet normalization + cell-value preview
// ============================================================================

// Accepts [string] or [{id, text|snippet|context|title, ...}] — returns
// [{id, text}] with ids defaulting to the array index.
function _normSnippets(snippets) {
  var out = [];
  if (!snippets) return out;
  for (var i = 0; i < snippets.length; i++) {
    var sn = snippets[i];
    if (sn === null || sn === undefined) continue;
    if (typeof sn === "string") {
      out.push({ id: String(i), text: sn });
      continue;
    }
    var text = sn.text || sn.snippet || sn.context || sn.summary || sn.title || sn.name;
    if (text === undefined || text === null) text = JSON.stringify(sn);
    out.push({ id: sn.id !== undefined && sn.id !== null ? String(sn.id) : String(i), text: String(text) });
  }
  return out;
}

// Compact preview for large cell returns — the full value stays in scratch
// (lossless); this is just what the root's context sees. Bounded by
// construction (head slices + counts), unlike displayValue/inferSchema whose
// container walks are non-truncating.
function _preview(v) {
  if (typeof v === "string") {
    return "string(" + v.length + "): " + JSON.stringify(v.substring(0, 600)) + "…";
  }
  if (Array.isArray(v)) {
    var head = v.length > 0 ? inferSchema(v[0]) : "";
    if (head.length > 800) head = head.substring(0, 800) + "…";
    return "array(" + v.length + "); first item: " + head;
  }
  if (v && typeof v === "object") {
    var keys = Object.keys(v);
    var parts = [];
    for (var i = 0; i < keys.length && i < 40; i++) {
      var k = keys[i];
      var t = Array.isArray(v[k]) ? "array(" + v[k].length + ")" : typeof v[k];
      parts.push(k + ": " + t);
    }
    return "object{" + parts.join(", ") + (keys.length > 40 ? ", …+" + (keys.length - 40) : "") + "}";
  }
  return String(v);
}

// Snippet text for an objects-scope candidate row: the name plus every
// string property value from the row's per-type namespaces — descriptions
// and the like are free semantic surface already present in the query row
// (NOT page content; content lives in editor blocks and needs hydrate —
// see the two-stage objects pattern in the system prompt).
function _objectRowText(row) {
  var parts = [row.name || ""];
  for (var ns in row) {
    if (!row.hasOwnProperty(ns)) continue;
    if (ns === "name" || ns === "id" || ns === "nav" || ns === "any" || ns === "_ver") continue;
    var bag = row[ns];
    if (!bag || typeof bag !== "object" || Array.isArray(bag)) continue;
    for (var k in bag) {
      if (!bag.hasOwnProperty(k)) continue;
      var v = bag[k];
      if (typeof v === "string" && v) parts.push(v.substring(0, 200));
      else if (Array.isArray(v) && v.length > 0 && typeof v[0] === "string") parts.push(v.join(", ").substring(0, 200));
    }
  }
  return parts.join(" — ").substring(0, 500);
}

// ============================================================================
// rlm.* primitives — bound per invocation; account into ctx.stats
// ============================================================================

function _makeRLM(ctx) {
  // candidates — the cheap generator stage, and the single vector-service
  // seam: when globalThis.__searchService exists it produces the candidates
  // (mode flips to rlm+semantic); until then lexical/index reads do.
  function candidates(scope, o) {
    o = o || {};
    var limit = o.limit || 50;
    var offset = o.offset || 0;

    var svc = null;
    try { svc = globalThis.__searchService; } catch (e) {}
    if (svc && typeof svc.query === "function" && (scope === "memory" || scope === "history")) {
      ctx.stats.semantic = true;
      var hits = svc.query({
        text: o.query || ctx.query,
        chatId: ctx.chatId || undefined,
        categories: o.categories,
        k: limit
      }) || [];
      return hits; // [{id, score, kind}] per the convmemory@v1 contract
    }

    if (scope === "memory") {
      var items;
      if (o.categories) items = ctx.conv.byCategory(o.categories, limit);
      else items = ctx.conv.recent(limit);
      var mOut = [];
      for (var i = 0; i < items.length; i++) {
        var it = items[i];
        mOut.push({
          id: it.id, kind: "item",
          title: it.context || "",
          text: (it.context || "") + (it.body ? " — " + String(it.body).substring(0, 300) : ""),
          category: it.category, createdAt: it.createdAt
        });
      }
      return mOut;
    }

    if (scope === "history") {
      if (!ctx.chatId) throw new Error("history scope needs a chatId (opts.chatId or invocation args)");
      var chunks = ctx.conv.listChunks(ctx.chatId, { limit: limit, offset: offset, order: "desc" });
      var hOut = [];
      for (var ci = 0; ci < chunks.length; ci++) {
        var ch = chunks[ci];
        hOut.push({
          id: "chunk:" + ch.seq, kind: "chunk", seq: ch.seq,
          title: "turns " + ch.fromSeq + ".." + ch.toSeq,
          text: ch.summary || "",
          fromSeq: ch.fromSeq, toSeq: ch.toSeq,
          periodStart: ch.periodStart, periodEnd: ch.periodEnd
        });
      }
      return hOut;
    }

    if (scope === "objects") {
      if (!o.type) throw new Error("candidates('objects', …) requires opts.type — list types from the corpus metadata, or probe with anyHelper.getTypes()");
      var q = { type: o.type, limit: limit, offset: offset };
      if (o.filter) q.filter = o.filter;
      if (o.sort) q.sort = o.sort;
      var rows = ctx.client.getObjects(q);
      var oOut = [];
      for (var ri = 0; ri < rows.length; ri++) {
        var row = rows[ri];
        oOut.push({ id: row.id, kind: "object", title: row.name || "", text: _objectRowText(row), type: o.type });
      }
      return oOut;
    }

    throw new Error("unknown candidates scope: " + scope + " (memory|history|objects)");
  }

  // Shared map core — one prompt per batch of `per` snippets, ALL prompts in
  // a single completeBatchDetailed round trip (one fetchBatch). Token usage
  // from every batched call lands in ctx.stats.
  function _mapLLM(snippets, instruction, perEntryFields, o) {
    o = o || {};
    var per = o.batch || CLASSIFY_BATCH;
    var norm = _normSnippets(snippets);
    if (norm.length === 0) return [];

    var prompts = [];
    var groups = [];
    for (var start = 0; start < norm.length; start += per) {
      var group = norm.slice(start, start + per);
      groups.push(group);
      var lines = [];
      for (var gi = 0; gi < group.length; gi++) {
        lines.push(gi + ". " + group[gi].text.replace(/\n/g, " "));
      }
      prompts.push(
        instruction + "\n\nSnippets:\n" + lines.join("\n") +
        "\n\nReply with ONLY a JSON array, one entry per snippet (all " + group.length + " of them), " +
        "each entry shaped: " + perEntryFields + ". No prose, no markdown fence."
      );
    }

    var responses = ctx.deps.batch(prompts, o.tier || "classify");
    ctx.stats.batchedSubCalls += 1;
    ctx.stats.subCalls += prompts.length;
    ctx.stats.objectsScanned += norm.length;

    var out = [];
    for (var bi = 0; bi < groups.length; bi++) {
      var resp = responses[bi] || {};
      ctx.stats.tokensIn += resp.inTokens || 0;
      ctx.stats.tokensOut += resp.outTokens || 0;
      var parsed = resp.text ? parseJSON(resp.text) : null;
      var byN = {};
      if (Array.isArray(parsed)) {
        for (var pi = 0; pi < parsed.length; pi++) {
          if (parsed[pi] && parsed[pi].n !== undefined) byN[parsed[pi].n] = parsed[pi];
        }
      }
      for (var gj = 0; gj < groups[bi].length; gj++) {
        var entry = byN[gj] || null;
        out.push({ id: groups[bi][gj].id, entry: entry });
      }
    }
    return out;
  }

  // mapClassify — score each snippet 0-10 against the query, with a one-line
  // why. Returns [{id, score, why}] in input order; unparsed → score null.
  function mapClassify(snippets, q, o) {
    var raw = _mapLLM(
      snippets,
      "Score each numbered snippet 0-10 for relevance to this query.\nQuery: " + (q || ctx.query),
      '{"n": <snippet number>, "score": <0-10>, "why": "<one line, ≤15 words>"}',
      o
    );
    var out = [];
    for (var i = 0; i < raw.length; i++) {
      var e = raw[i].entry;
      out.push({
        id: raw[i].id,
        score: e && typeof e.score === "number" ? e.score : null,
        why: e && e.why ? String(e.why) : "(unparsed)"
      });
    }
    return out;
  }

  // mapExtract — pull what each snippet says about the question (synthesis
  // feed). Returns [{id, extract}] in input order; null when nothing/unparsed.
  function mapExtract(snippets, question, o) {
    var raw = _mapLLM(
      snippets,
      "For each numbered snippet, extract what it says that is relevant to this question " +
      "(verbatim-ish, compact). If a snippet says nothing relevant, use null.\nQuestion: " + (question || ctx.query),
      '{"n": <snippet number>, "extract": "<relevant content>" | null}',
      o
    );
    var out = [];
    for (var i = 0; i < raw.length; i++) {
      var e = raw[i].entry;
      out.push({ id: raw[i].id, extract: e && e.extract ? String(e.extract) : null });
    }
    return out;
  }

  // hydrate — full records for the ranked top-k ONLY. refs: [{id, kind, seq?}]
  // (candidates output objects work as-is).
  function hydrate(refs) {
    var out = [];
    if (!refs) return out;
    for (var i = 0; i < refs.length; i++) {
      var ref = refs[i];
      if (!ref) continue;
      var kind = ref.kind || "object";
      try {
        if (kind === "item") {
          var rows = ctx.conv.memoryQuery({ id: ref.id }, null, 1);
          out.push(rows.length > 0 ? rows[0] : null);
        } else if (kind === "chunk") {
          var seq = ref.seq !== undefined ? ref.seq : parseInt(String(ref.id).replace("chunk:", ""), 10);
          out.push(ctx.conv.expandChunk(ctx.chatId, seq));
        } else if (kind === "turn") {
          var tseq = ref.seq !== undefined ? ref.seq : parseInt(String(ref.id), 10);
          var turns = ctx.conv.turnRange(ctx.chatId, tseq, tseq);
          out.push(turns.length > 0 ? turns[0] : null);
        } else {
          out.push(ctx.client.getObject(ref.id));
        }
      } catch (e) {
        out.push({ id: ref.id, error: String((e && e.message) || e) });
      }
    }
    return out;
  }

  // final — the termination sentinel. The host returns right after the cell
  // that calls this.
  function final(obj) {
    ctx.state.final = obj || {};
    return "final accepted — search will return after this cell";
  }

  return {
    candidates: candidates,
    mapClassify: mapClassify,
    mapExtract: mapExtract,
    hydrate: hydrate,
    final: final
  };
}

// ============================================================================
// Corpus metadata — counts and schemas only, never data (the RLM's
// Metadata(state): constant-size environment description for the root)
// ============================================================================

function _corpusMeta(ctx, scope) {
  var lines = [];

  if (scope === "memory" || scope === "auto") {
    try {
      var total = null;
      var probe = ctx.client.getObjects({
        objectId: ctx.conv.brainId(), dataset: "agent_memory_items",
        limit: 1, includeTotal: true
      });
      if (probe && probe.total !== undefined) total = probe.total;
      var cats = ctx.conv.listCategories();
      var catParts = [];
      for (var i = 0; i < cats.observed.length; i++) {
        catParts.push(cats.observed[i].name + "(" + cats.observed[i].count + ")");
      }
      lines.push("memory: " + (total !== null ? total : "?") + " items on the space brain. " +
        "Categories observed: " + (catParts.length ? catParts.join(", ") : "(none yet)") + ". " +
        "Builtin categories: " + cats.builtin.join(", ") + ".");
    } catch (e) {
      lines.push("memory: unavailable (" + String((e && e.message) || e) + ")");
    }
  }

  if ((scope === "history" || scope === "auto") && ctx.chatId) {
    try {
      var turnsProbe = ctx.client.getObjects({ objectId: ctx.chatId, dataset: "agent_turns", limit: 1, includeTotal: true });
      var chunksProbe = ctx.client.getObjects({ objectId: ctx.chatId, dataset: "agent_chunks", limit: 1, includeTotal: true });
      lines.push("history: " + (turnsProbe.total !== undefined ? turnsProbe.total : "?") + " raw turns, " +
        (chunksProbe.total !== undefined ? chunksProbe.total : "?") + " summary chunks on chat " + ctx.chatId + ". " +
        "Each chunk summarizes ~10 turns and carries fromSeq..toSeq drill-down pointers.");
    } catch (e) {
      lines.push("history: unavailable (" + String((e && e.message) || e) + ")");
    }
  }

  if (scope === "objects" || scope === "auto") {
    try {
      var types = ctx.client.getTypes();
      var names = [];
      for (var ti = 0; ti < types.length; ti++) {
        names.push(types[ti].xKey || types[ti].name || types[ti].id);
      }
      lines.push("objects: types in this space: " + names.join(", ") + ". " +
        "Counts via anyHelper.getObjects({type, limit: 1, includeTotal: true}).total; " +
        "schema via anyHelper.describeType(typeKey).");
    } catch (e) {
      lines.push("objects: type list unavailable (" + String((e && e.message) || e) + ")");
    }
  }

  return lines.join("\n");
}

// ============================================================================
// Root system prompt — role, environment, cell contract, decomposition
// examples (the paper's Fig-4a lever: iterate HERE first), output contract
// ============================================================================

function _buildSystemPrompt(ctx, scope, synthesize) {
  var p = [];

  p.push(
    "You are a SEARCH SUBROUTINE running inside another agent's request. There is no user; " +
    "you cannot send messages. Your only job: find the most relevant records for the query " +
    (synthesize ? "AND synthesize an answer with citations, " : "") +
    "then call rlm.final({...}) in a cell. Plain-text replies go nowhere — only rlm.final returns data."
  );

  p.push("## Corpus (metadata only — page the data through cells, never assume contents)\n" + _corpusMeta(ctx, scope));

  p.push(
    "## Cell contract\n" +
    "Each run_cell body is a JavaScript FUNCTION BODY with these parameters bound:\n" +
    "- rlm — primitives below\n" +
    "- s — scratch object persisting across YOUR cells (s.x = ... survives; plain `var` does not)\n" +
    "- anyHelper — getObjects({type|objectId, dataset, filter, sort, limit, offset, includeTotal}), " +
    "getObject(id), describeType(typeKey), getTypes(); filters support $regex/$in/$gte (see docs/09-query.md operators)\n" +
    "- convmemory — byCategory/byPeriod/recent/memoryQuery, listChunks/expandChunk/turnRange/turnsByPeriod\n" +
    "- llm — classify(prompt)/chat(messages, opts) for one-off judgments; use rlm.mapClassify for anything bulk\n" +
    "- query — the search query string\n" +
    "Rules: `return <value>` to see a value (NO implicit last-expression). ALWAYS `var` — bare assignment " +
    "leaks into a shared kernel. No import. Large returns are kept lossless in s._<n> with a preview shown to you.\n" +
    "\n" +
    "## rlm primitives\n" +
    "- rlm.candidates(scope, {query?, categories?, type?, filter?, sort?, limit?, offset?}) → cheap [{id, kind, title, text, ...}] " +
    "(ids+snippet text only; objects scope REQUIRES type)\n" +
    "- rlm.mapClassify(snippets, query, {batch?}) → [{id, score: 0-10|null, why}] — batched haiku map; THE bulk relevance tool\n" +
    "- rlm.mapExtract(snippets, question, {batch?}) → [{id, extract|null}] — for synthesis\n" +
    "- rlm.hydrate(refs) → full records, top-k ONLY (refs need {id, kind})\n" +
    "- rlm.final({results, answer?, citations?, note?}) → ends the search"
  );

  p.push(
    "## Decomposition patterns (worked examples)\n" +
    "\n" +
    "Memory recall — candidates → mapClassify → rank → final:\n" +
    "```\n" +
    "var cands = rlm.candidates('memory', {limit: 200});\n" +
    "var scored = rlm.mapClassify(cands, query);\n" +
    "var byId = {}; cands.forEach(function(c){ byId[c.id] = c; });\n" +
    "scored.sort(function(a,b){ return (b.score||0) - (a.score||0); });\n" +
    "s.top = scored.slice(0, 8).filter(function(r){ return (r.score||0) >= 5; });\n" +
    "rlm.final({results: s.top.map(function(r){ var c = byId[r.id];\n" +
    "  return {id: r.id, kind: 'item', score: r.score, title: c.title, snippet: c.text, why: r.why}; })});\n" +
    "```\n" +
    "Temporal query? Use convmemory.byPeriod(fromUnix, untilUnix, categories, limit) to slice first, then mapClassify the slice.\n" +
    "\n" +
    "History — coarse-to-fine through the chunk layer (NEVER scan raw turns first):\n" +
    "```\n" +
    "var chunks = rlm.candidates('history', {limit: 50});           // chunk summaries ≈ 10 turns each\n" +
    "var scored = rlm.mapClassify(chunks, query);\n" +
    "var promising = scored.filter(function(r){ return (r.score||0) >= 6; }).slice(0, 3);\n" +
    "var byId = {}; chunks.forEach(function(c){ byId[c.id] = c; });\n" +
    "s.expanded = rlm.hydrate(promising.map(function(r){ return byId[r.id]; }));  // raw turns inside\n" +
    "// then mapClassify the raw turns (turn.userText + turn.replies) and final the winners\n" +
    "```\n" +
    "\n" +
    "Objects — TWO-STAGE coarse-to-fine. Candidates carry only name + string properties; " +
    "page CONTENT lives in editor blocks and only appears after rlm.hydrate. So: scan titles/props " +
    "with a GENEROUS gate (titles undersell content), hydrate the survivors, classify their content:\n" +
    "```\n" +
    "var total = anyHelper.getObjects({type: 'pages', limit: 1, includeTotal: true}).total;\n" +
    "var page = rlm.candidates('objects', {type: 'pages', limit: 100, offset: 0});\n" +
    "// distinctive token? narrow cheaply first:\n" +
    "// rlm.candidates('objects', {type: 'pages', filter: {name: {$regex: 'lexid'}}, limit: 100})\n" +
    "var coarse = rlm.mapClassify(page, query);\n" +
    "var byId = {}; page.forEach(function(c){ byId[c.id] = c; });\n" +
    "coarse.sort(function(a,b){ return (b.score||0)-(a.score||0); });\n" +
    "var survivors = coarse.filter(function(r){ return (r.score||0) >= 3; }).slice(0, 12);  // generous\n" +
    "var full = rlm.hydrate(survivors.map(function(r){ return byId[r.id]; }));               // → .markdown\n" +
    "var snippets = full.map(function(o){ return {id: o.id,\n" +
    "  text: (o.name||'') + '\\n' + String(o.markdown||'').substring(0, 1500)}; });\n" +
    "var fine = rlm.mapClassify(snippets, query);  // CONTENT scores — rank/final from these,\n" +
    "// snippet = the content region that matched, not the title\n" +
    "// loop offset += 100 until total covered OR you already have k confident hits (score >= 7) — then STOP\n" +
    "```\n" +
    "\n" +
    "Stop scanning as soon as you have enough confident hits. If a scan is large, keep intermediate " +
    "arrays in s.* and aggregate across cells — never re-fetch what you already paged."
  );

  var resultShape = "{id, kind: 'item'|'chunk'|'turn'|'object', score, title, snippet, why" +
    (synthesize ? ", source?" : "") + "}";
  p.push(
    "## Output contract\n" +
    "rlm.final({\n" +
    "  results: [" + resultShape + "],   // ranked, best first, up to k=" + (ctx.opts.k || DEFAULT_K) + "\n" +
    (synthesize
      ? "  answer: '<COMPLETE, self-contained answer (2-4 sentences), grounded ONLY in scanned records — " +
        "state the facts themselves, NEVER point at a snippet or say \"see above\">',\n" +
        "  citations: [{id}],               // the result ids backing the answer\n"
      : "") +
    "  note: '<coverage: what was scanned, what was not, why>'  // honest accounting, required if you did not cover everything\n" +
    "})\n" +
    "If you receive a WRAP-UP message: caps are reached — emit rlm.final IMMEDIATELY in your next cell " +
    "with best-so-far results and a note stating what remained unscanned."
  );

  return p.join("\n\n");
}

// ============================================================================
// Cell execution + result formatting
// ============================================================================

function _execCell(code, binds) {
  var fn;
  try {
    fn = new Function("rlm", "s", "anyHelper", "convmemory", "llm", "query", code);
  } catch (e) {
    return { error: "Syntax error: " + String((e && e.message) || e) };
  }
  try {
    var v = fn(binds.rlm, binds.s, binds.anyHelper, binds.convmemory, binds.llm, binds.query);
    return { value: v };
  } catch (e) {
    return { error: String((e && e.message) || e) };
  }
}

function _formatCellResult(res, s, n) {
  if (res.error) return "Error: " + res.error;
  if (res.value === undefined) {
    return "(cell returned undefined — use `return <value>` to surface data, or rlm.final to finish)";
  }
  s["_" + n] = res.value; // lossless: full value always retrievable from scratch
  var full;
  try { full = displayValue(res.value); } catch (e) { full = String(res.value); }
  if (full.length <= INLINE_VALUE_CHARS) {
    return "Returned: " + full;
  }
  return "Returned value is large — kept LOSSLESS in s._" + n + " (use it from your next cell). Preview: " + _preview(res.value);
}

function _findBlocks(content, type) {
  var out = [];
  if (!content) return out;
  for (var i = 0; i < content.length; i++) {
    if (content[i] && content[i].type === type) out.push(content[i]);
  }
  return out;
}

function _contextChars(messages) {
  try { return JSON.stringify(messages).length; } catch (e) { return 0; }
}

function _addUsage(stats, usage) {
  if (!usage) return;
  stats.tokensIn += usage.input_tokens || usage.prompt_tokens || 0;
  stats.tokensOut += usage.output_tokens || usage.completion_tokens || 0;
}

// ============================================================================
// Result assembly
// ============================================================================

function _normalizeResults(raw, ctx, k) {
  var out = [];
  if (!Array.isArray(raw)) return out;
  for (var i = 0; i < raw.length && out.length < k; i++) {
    var r = raw[i];
    if (!r) continue;
    var kind = r.kind || "object";
    var rec = {
      id: r.id !== undefined ? String(r.id) : "",
      kind: kind,
      score: typeof r.score === "number" ? r.score : null,
      title: r.title !== undefined ? String(r.title) : "",
      snippet: r.snippet !== undefined ? String(r.snippet) : (r.text !== undefined ? String(r.text) : ""),
      why: r.why !== undefined ? String(r.why) : ""
    };
    // source: host object the record lives on — memory items sit on the brain,
    // chunks/turns on the chat, objects are their own host.
    var hostId = null;
    if (kind === "item") { try { hostId = ctx.conv.brainId(); } catch (e) {} }
    else if (kind === "chunk" || kind === "turn") hostId = ctx.chatId;
    else hostId = rec.id.indexOf("chunk:") === 0 ? null : rec.id;
    rec.source = r.source || {
      objectId: hostId,
      link: hostId && ctx.spaceId ? "any://" + ctx.spaceId + "/" + hostId : null
    };
    out.push(rec);
  }
  return out;
}

// Deterministic recency fallback when the root never produced a final —
// best-effort recall, honestly labeled. Never throws.
function _fallbackRecency(ctx, scope, k) {
  try {
    if (scope === "history" && ctx.chatId) {
      var chunks = ctx.conv.listChunks(ctx.chatId, { limit: k, order: "desc" });
      var hOut = [];
      for (var i = 0; i < chunks.length; i++) {
        hOut.push({ id: "chunk:" + chunks[i].seq, kind: "chunk", score: null, title: "turns " + chunks[i].fromSeq + ".." + chunks[i].toSeq, snippet: chunks[i].summary || "", why: "recency fallback" });
      }
      return hOut;
    }
    // memory + objects-without-type + auto all fall back to recent memory —
    // the one corpus with a meaningful recency default.
    var items = ctx.conv.recent(k);
    var out = [];
    for (var j = 0; j < items.length; j++) {
      out.push({ id: items[j].id, kind: "item", score: null, title: items[j].context || "", snippet: items[j].context || "", why: "recency fallback" });
    }
    return out;
  } catch (e) {
    return [];
  }
}

// ============================================================================
// The root loop
// ============================================================================

function _runRoot(query, opts, deps) {
  var t0 = Date.now();
  var stats = {
    rootTurns: 0, toolcalls: 0, subCalls: 0, batchedSubCalls: 0,
    objectsScanned: 0, tokensIn: 0, tokensOut: 0, ms: 0,
    wrappedUp: false, semantic: false
  };
  var scope = opts.scope || "auto";
  var k = opts.k || DEFAULT_K;

  var ctx = {
    client: deps.client, conv: deps.conv, deps: deps,
    spaceId: deps.spaceId, chatId: opts.chatId || deps.chatId || null,
    query: query, opts: opts,
    stats: stats, state: { final: null }
  };

  var scratch = {};
  var binds = {
    rlm: _makeRLM(ctx),
    s: scratch,
    anyHelper: deps.client,
    convmemory: deps.conv,
    llm: deps.llm,
    query: query
  };

  function finish(mode, results, extra) {
    stats.ms = Date.now() - t0;
    delete stats.semantic; // internal flag, folded into mode
    var out = {
      ok: mode !== "error",
      mode: mode,
      scope: scope,
      results: results || [],
      stats: stats
    };
    if (extra) {
      for (var key in extra) {
        if (extra.hasOwnProperty(key) && extra[key] !== undefined && extra[key] !== null) out[key] = extra[key];
      }
    }
    return out;
  }

  function assembleFinal() {
    var fin = ctx.state.final;
    var mode = stats.semantic ? "rlm+semantic" : "rlm";
    var extra = { note: fin.note };
    if (opts.synthesize) {
      extra.answer = fin.answer !== undefined ? String(fin.answer) : undefined;
      if (Array.isArray(fin.citations)) extra.citations = fin.citations;
    }
    return finish(mode, _normalizeResults(fin.results, ctx, k), extra);
  }

  function assembleFallback(why) {
    return finish("fallback-recency", _fallbackRecency(ctx, scope, k), {
      note: why + " — returning recency fallback. See stats for what was burned."
    });
  }

  var system;
  try {
    system = _buildSystemPrompt(ctx, scope, !!opts.synthesize);
  } catch (e) {
    return finish("error", [], { error: "system prompt build failed: " + String((e && e.message) || e) });
  }

  var maxTc = opts.maxToolcalls || deps.maxToolcalls || MAX_TOOLCALLS;
  var maxTurns = opts.maxRootTurns || deps.maxRootTurns || MAX_ROOT_TURNS;
  var ceiling = deps.contextCeilingChars || CONTEXT_CEILING_CHARS;
  var hardStop = maxTurns + (HARD_TURN_STOP - MAX_ROOT_TURNS);

  var askLine = (opts.synthesize ? "Question" : "Search request") + ": " + query;
  if (scope !== "auto") askLine += "\nScope: " + scope;
  if (opts.type) askLine += "\nNarrow to type: " + opts.type;
  if (opts.categories) askLine += "\nCategories: " + JSON.stringify(opts.categories);
  if (opts.periodFrom || opts.periodUntil) askLine += "\nPeriod: " + (opts.periodFrom || "…") + " → " + (opts.periodUntil || "…");
  askLine += "\nReturn up to k=" + k + " results via rlm.final.";
  var messages = [{ role: "user", content: askLine }];

  var WRAPUP = "WRAP-UP: caps reached (toolcalls/turns/context). Run ONE more cell that calls " +
    "rlm.final NOW with your best-so-far results and a note stating exactly what was and wasn't scanned.";
  var wrapupIssued = false;
  var nudged = false;

  while (true) {
    if (stats.rootTurns >= hardStop) {
      stats.wrappedUp = true;
      return assembleFallback("hard turn stop (" + hardStop + ") without rlm.final");
    }

    var resp;
    try {
      resp = deps.llm.chat(messages, { system: system, tools: [CELL_TOOL], tier: deps.rootTier || "reason" });
    } catch (e) {
      return finish("error", [], { error: "root LLM call failed on turn " + (stats.rootTurns + 1) + ": " + String((e && e.message) || e) });
    }
    stats.rootTurns++;
    _addUsage(stats, resp && resp.usage);

    if (!resp || !resp.content) {
      return finish("error", [], { error: "empty root LLM response on turn " + stats.rootTurns });
    }
    messages.push({ role: "assistant", content: resp.content });

    var toolBlocks = _findBlocks(resp.content, "tool_use");

    if (resp.stop_reason === "max_tokens") {
      // pair every tool_use with an error result, then wrap up
      var cut = [];
      for (var mi = 0; mi < toolBlocks.length; mi++) {
        cut.push({ type: "tool_result", tool_use_id: toolBlocks[mi].id, content: "Cell not executed — response cut off by max_tokens.", is_error: true });
      }
      if (cut.length > 0) messages.push({ role: "user", content: cut });
      if (wrapupIssued) return assembleFallback("max_tokens after wrap-up");
      wrapupIssued = true;
      stats.wrappedUp = true;
      messages.push({ role: "user", content: WRAPUP });
      continue;
    }

    if (toolBlocks.length === 0) {
      // text-only turn: done already? nudge once; after wrap-up there's nothing left to wait for
      if (ctx.state.final) return assembleFinal();
      if (nudged || wrapupIssued) {
        return assembleFallback("root ended (" + (resp.stop_reason || "no stop_reason") + ") without rlm.final");
      }
      nudged = true;
      messages.push({ role: "user", content: "You are not done until a cell calls rlm.final({results: [...]}). Run that cell now." });
      continue;
    }

    var toolResults = [];
    for (var tbi = 0; tbi < toolBlocks.length; tbi++) {
      if (ctx.state.final) {
        // final already set by an earlier block this turn — every tool_use
        // still needs a paired tool_result
        toolResults.push({ type: "tool_result", tool_use_id: toolBlocks[tbi].id, content: "(skipped — rlm.final already set)" });
        continue;
      }
      stats.toolcalls++;
      var code = toolBlocks[tbi].input && toolBlocks[tbi].input.code;
      var res = code ? _execCell(code, binds) : { error: "empty code in run_cell input" };
      toolResults.push({
        type: "tool_result",
        tool_use_id: toolBlocks[tbi].id,
        content: _formatCellResult(res, scratch, stats.toolcalls),
        is_error: !!res.error
      });
    }
    messages.push({ role: "user", content: toolResults });

    if (ctx.state.final) return assembleFinal();

    if (wrapupIssued) {
      // the wrap-up turn ran its cells and still didn't final
      return assembleFallback("wrap-up turn produced no rlm.final");
    }
    if (stats.toolcalls >= maxTc || stats.rootTurns >= maxTurns || _contextChars(messages) >= ceiling) {
      wrapupIssued = true;
      stats.wrappedUp = true;
      messages.push({ role: "user", content: WRAPUP });
    }
  }
}

// ============================================================================
// Public surface
// ============================================================================

// createSearch(deps?) — factory, primarily for tests: every collaborator the
// loop touches is injectable. Defaults wire the live environment.
//   deps: { client?, conv?, llm?, batch?, spaceId?, chatId?,
//           maxToolcalls?, maxRootTurns?, contextCeilingChars?, rootTier? }
export function createSearch(deps) {
  deps = deps || {};
  var client = deps.client;
  if (!client) {
    client = createClient({
      apiBaseUrl: env.ANYTYPE_API_URL,
      apiKey: env.ANYTYPE_API_KEY,
      spaceId: env.ANYTYPE_SPACE_ID,
      noTrace: true
    });
  }
  var resolved = {
    client: client,
    conv: deps.conv || createConvMemory(client),
    llm: deps.llm || createLLM(),
    batch: deps.batch || completeBatchDetailed,
    spaceId: deps.spaceId || (typeof env !== "undefined" && env.ANYTYPE_SPACE_ID) || null,
    chatId: deps.chatId || _argsChatId(),
    maxToolcalls: deps.maxToolcalls,
    maxRootTurns: deps.maxRootTurns,
    contextCeilingChars: deps.contextCeilingChars,
    rootTier: deps.rootTier
  };

  function search(query, opts) {
    if (!query || typeof query !== "string") {
      return { ok: false, mode: "error", error: "query (string) is required", results: [], stats: null };
    }
    return _runRoot(query, opts || {}, resolved);
  }

  function ask(question, opts) {
    var o = {};
    if (opts) {
      for (var key in opts) {
        if (opts.hasOwnProperty(key)) o[key] = opts[key];
      }
    }
    o.synthesize = true;
    return search(question, o);
  }

  return { search: search, ask: ask };
}

function _argsChatId() {
  try {
    if (typeof args !== "undefined" && args && args.chatId) return args.chatId;
  } catch (e) {}
  try {
    if (globalThis.args && globalThis.args.chatId) return globalThis.args.chatId;
  } catch (e) {}
  return null;
}

// Tool-facing methods — lazily build the default instance so module import
// stays cheap (boot prelude imports every tool).
var _defaultInstance = null;
function _getDefault() {
  if (!_defaultInstance) _defaultInstance = createSearch();
  return _defaultInstance;
}

export function search(query, opts) {
  return _getDefault().search(query, opts);
}

export function ask(question, opts) {
  return _getDefault().ask(question, opts);
}
