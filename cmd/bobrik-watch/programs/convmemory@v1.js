// convmemory@v1 — conversation history + typed memory over the server's
// agent data layer (docs/11-agent-memory.md). Replaces amemory@v2.
//
// Storage (all server-side built-in datasets, validated + indexed):
//   agent_turns         on the CHAT OBJECT — one append-only record per agent
//                       invocation (seq, userText, think, replies[], effects[],
//                       messageIds[], debugRef, llm scalars)
//   agent_chunks        on the CHAT OBJECT — immutable summaries with EXPLICIT
//                       pointers (fromSeq..toSeq) into the raw turn range
//   agent_memory_items  on the per-space BRAIN OBJECT (GET /agent/brain) —
//                       typed items: category/context/body, tags/entities/
//                       keywords arrays, confidence/importance/salience
//
// Reads are getObjects({objectId, dataset, filter, sort, limit}) — indexed
// range queries, never full loads. Writes go through the bespoke /agent/*
// endpoints via client.api (validation + server stamping).
//
// SEMANTIC SEARCH IS EXTERNALIZED — TODO, NOT FUNCTIONAL YET. There are no
// vectors here (the old hex-embeddings + JS cosine full-scan are gone). A
// separate vector service will tail /query/subscribe and answer hybrid recall
// via globalThis.__searchService (see search() below). Until it lands,
// search() falls back to non-semantic indexed paths: explicit/detected
// period → category → recency.

import { createClient } from "anyHelper@v1";

export var MEMORY_CATEGORIES = ["claim", "preference", "decision", "lesson", "episode", "taskstate", "insight"];

// ============================================================================
// createConvMemory(client) — instance factory. toolcall_core uses this with
// its bootClient; the module-level tool surface below uses a lazy internal
// client.
// ============================================================================

export function createConvMemory(client) {
  var spacePath = "/v1/spaces/" + client.config.spaceId;
  var _brainId = null;

  // Resolve (and memoize) the deterministic per-space brain object id.
  function brainId() {
    if (_brainId) return _brainId;
    var res = client.api("GET", spacePath + "/agent/brain", null);
    if (!res.ok || !res.data || !res.data.objectId) {
      throw new Error("convmemory: GET /agent/brain failed: " + (res.error || "no objectId"));
    }
    _brainId = res.data.objectId;
    return _brainId;
  }

  // --- turns ----------------------------------------------------------------

  // Newest `limit` turns, returned ASCENDING (oldest first — render order).
  function recentTurns(chatObjId, limit) {
    var rows = client.getObjects({
      objectId: chatObjId, dataset: "agent_turns",
      sort: ["-seq"], limit: limit || 8
    });
    return rows.reverse();
  }

  // Inclusive raw range — the chunk drill-down query.
  function turnRange(chatObjId, fromSeq, toSeq) {
    return client.getObjects({
      objectId: chatObjId, dataset: "agent_turns",
      filter: { seq: { "$gte": fromSeq, "$lte": toSeq } },
      sort: ["seq"]
    });
  }

  // Indexed createdAt range (unix seconds, until exclusive).
  function turnsByPeriod(chatObjId, fromUnix, untilUnix) {
    return client.getObjects({
      objectId: chatObjId, dataset: "agent_turns",
      filter: { createdAt: { "$gte": fromUnix, "$lt": untilUnix } },
      sort: ["createdAt"]
    });
  }

  // Append one immutable turn record. rec carries the
  // api.AgentTurnAppendRequest fields (seq required). A duplicate seq is
  // rejected server-side (append-only handler) — callers treat that as a
  // collision and re-probe.
  function appendTurn(chatObjId, rec) {
    var res = client.api("POST", spacePath + "/objects/" + chatObjId + "/agent/turns", rec);
    if (!res.ok) return { ok: false, error: res.error, code: res.code };
    return { ok: true, seq: rec.seq, recordId: res.data && res.data.recordIds && res.data.recordIds[0] };
  }

  // Highest stored seq, or -1 when the log is empty. One indexed probe.
  function lastSeq(chatObjId) {
    var rows = client.getObjects({
      objectId: chatObjId, dataset: "agent_turns",
      sort: ["-seq"], limit: 1
    });
    return rows.length > 0 ? rows[0].seq : -1;
  }

  // --- chunks ---------------------------------------------------------------

  // Newest `limit` chunks, ASCENDING.
  function recentChunks(chatObjId, limit) {
    var rows = client.getObjects({
      objectId: chatObjId, dataset: "agent_chunks",
      sort: ["-seq"], limit: limit || 8
    });
    return rows.reverse();
  }

  function listChunks(chatObjId, opts) {
    opts = opts || {};
    var q = {
      objectId: chatObjId, dataset: "agent_chunks",
      sort: [opts.order === "asc" ? "seq" : "-seq"],
      limit: opts.limit || 20
    };
    if (opts.offset) q.offset = opts.offset;
    return client.getObjects(q);
  }

  // The newest chunk, or null. Drives the compression trigger (its toSeq is
  // the high-water mark of compressed turns).
  function lastChunk(chatObjId) {
    var rows = client.getObjects({
      objectId: chatObjId, dataset: "agent_chunks",
      sort: ["-seq"], limit: 1
    });
    return rows.length > 0 ? rows[0] : null;
  }

  // Create one immutable chunk. rec: {seq, summary, periodStart, periodEnd,
  // fromSeq, toSeq, turnsCovered}.
  function createChunk(chatObjId, rec) {
    var res = client.api("POST", spacePath + "/objects/" + chatObjId + "/agent/chunks", rec);
    if (!res.ok) return { ok: false, error: res.error, code: res.code };
    return { ok: true, seq: rec.seq };
  }

  // chunk → its raw turns. The headline drill-down.
  function expandChunk(chatObjId, chunkSeq) {
    var rows = client.getObjects({
      objectId: chatObjId, dataset: "agent_chunks",
      filter: { seq: chunkSeq }, limit: 1
    });
    if (rows.length === 0) return { ok: false, error: "chunk seq " + chunkSeq + " not found" };
    var chunk = rows[0];
    return { ok: true, chunk: chunk, turns: turnRange(chatObjId, chunk.fromSeq, chunk.toSeq) };
  }

  // --- memory items ---------------------------------------------------------

  // Typed write — category + context REQUIRED (the agent classifies; there is
  // no LLM auto-extraction hop anymore). Returns {ok, id}.
  function addMemory(text, opts) {
    opts = opts || {};
    if (!opts.category) return { ok: false, error: "opts.category is required (e.g. \"preference\", \"lesson\")" };
    if (!opts.context) return { ok: false, error: "opts.context is required (one-line summary string)" };
    var body = {
      category: String(opts.category).toLowerCase(),
      context: opts.context,
      body: text || ""
    };
    if (opts.tags) body.tags = opts.tags;
    if (opts.entities) body.entities = opts.entities;
    if (opts.keywords) body.keywords = opts.keywords;
    if (opts.confidence !== undefined) body.confidence = opts.confidence;
    if (opts.importance !== undefined) body.importance = opts.importance;
    if (opts.edges) body.edges = opts.edges;
    if (opts.chatId) body.chatId = opts.chatId;
    var res = client.api("POST", spacePath + "/agent/memory", body);
    if (!res.ok) return { ok: false, error: res.error, code: res.code };
    return { ok: true, id: res.data && res.data.recordIds && res.data.recordIds[0], category: body.category, context: body.context };
  }

  // Evolve allow-listed mutable fields (salience, accessCount, confidence,
  // importance, context, body, tags, edges). Author-only.
  function evolveMemory(itemId, fields) {
    var res = client.api("PATCH", spacePath + "/agent/memory/" + itemId, fields || {});
    if (!res.ok) return { ok: false, error: res.error, code: res.code };
    return { ok: true, id: itemId };
  }

  function deleteMemory(itemId) {
    var res = client.api("DELETE", spacePath + "/agent/memory/" + itemId, null);
    if (!res.ok) return { ok: false, error: res.error, code: res.code };
    return { ok: true, id: itemId };
  }

  function memoryQuery(filter, sort, limit) {
    var q = { objectId: brainId(), dataset: "agent_memory_items", sort: sort || ["-createdAt"], limit: limit || 20 };
    if (filter && Object.keys(filter).length > 0) q.filter = filter;
    return client.getObjects(q);
  }

  function byCategory(categories, limit) {
    var cats = Array.isArray(categories) ? categories : [categories];
    return memoryQuery({ category: { "$in": cats } }, ["-createdAt"], limit);
  }

  // validFrom range (unix seconds, until exclusive), optional category narrow.
  function byPeriod(fromUnix, untilUnix, categories, limit) {
    var filter = { validFrom: { "$gte": fromUnix, "$lt": untilUnix } };
    if (categories && categories.length > 0) {
      filter.category = { "$in": Array.isArray(categories) ? categories : [categories] };
    }
    return memoryQuery(filter, ["validFrom"], limit);
  }

  function recent(limit, categories) {
    var filter = null;
    if (categories && categories.length > 0) {
      filter = { category: { "$in": Array.isArray(categories) ? categories : [categories] } };
    }
    return memoryQuery(filter, ["-createdAt"], limit);
  }

  // {builtin, observed} — observed = distinct categories actually stored.
  // The dataset has no facet query; scan a bounded recent window for the
  // inventory (the prompt section only needs names, not exact counts).
  function listCategories() {
    var rows = memoryQuery(null, ["-createdAt"], 500);
    var counts = {};
    for (var i = 0; i < rows.length; i++) {
      var c = rows[i].category;
      if (!c) continue;
      counts[c] = (counts[c] || 0) + 1;
    }
    var builtinSet = {};
    for (var bi = 0; bi < MEMORY_CATEGORIES.length; bi++) builtinSet[MEMORY_CATEGORIES[bi]] = true;
    var observed = [];
    for (var name in counts) {
      observed.push({ name: name, count: counts[name], builtin: !!builtinSet[name] });
    }
    observed.sort(function(a, b) { return b.count - a.count; });
    return { builtin: MEMORY_CATEGORIES.slice(), observed: observed };
  }

  return {
    brainId: brainId,
    recentTurns: recentTurns,
    turnRange: turnRange,
    turnsByPeriod: turnsByPeriod,
    appendTurn: appendTurn,
    lastSeq: lastSeq,
    recentChunks: recentChunks,
    listChunks: listChunks,
    lastChunk: lastChunk,
    createChunk: createChunk,
    expandChunk: expandChunk,
    addMemory: addMemory,
    evolveMemory: evolveMemory,
    deleteMemory: deleteMemory,
    memoryQuery: memoryQuery,
    byCategory: byCategory,
    byPeriod: byPeriod,
    recent: recent,
    listCategories: listCategories
  };
}

// ============================================================================
// Temporal reference detection — pure date math, ported from amemory@v2.
// Returns {from, until} ISO strings or null.
// ============================================================================

export function detectTemporalReference(queryText) {
  if (!queryText) return null;
  var q = queryText.toLowerCase();
  var now = new Date();
  var y = now.getFullYear();
  var mo = now.getMonth();

  function pad2(n) { return (n < 10) ? "0" + n : "" + n; }
  function dayStart(date) {
    return date.getFullYear() + "-" + pad2(date.getMonth() + 1) + "-" + pad2(date.getDate()) + "T00:00:00";
  }
  function dayEnd(date) {
    return date.getFullYear() + "-" + pad2(date.getMonth() + 1) + "-" + pad2(date.getDate()) + "T23:59:59";
  }
  function nowISO() { return now.toISOString().substring(0, 19); }
  function daysAgo(n) { return new Date(now.getTime() - n * 24 * 60 * 60 * 1000); }

  if (q.indexOf("yesterday") !== -1) {
    var yd = daysAgo(1);
    return { from: dayStart(yd), until: dayEnd(yd) };
  }
  if (q.indexOf("today") !== -1) {
    return { from: dayStart(now), until: nowISO() };
  }
  var daysAgoIdx = q.indexOf("days ago");
  if (daysAgoIdx !== -1) {
    var numStr = "";
    for (var di = daysAgoIdx - 1; di >= 0; di--) {
      var ch = q.charAt(di);
      if (ch >= "0" && ch <= "9") { numStr = ch + numStr; }
      else if (ch === " " && numStr.length > 0) break;
      else if (ch !== " ") break;
    }
    var nDays = parseInt(numStr, 10);
    if (!isNaN(nDays) && nDays > 0 && nDays < 365) {
      var nd = daysAgo(nDays);
      return { from: dayStart(nd), until: dayEnd(nd) };
    }
  }
  if (q.indexOf("last week") !== -1) {
    return { from: dayStart(daysAgo(7)), until: nowISO() };
  }
  if (q.indexOf("this week") !== -1) {
    var dow = now.getDay();
    var mondayOffset = (dow === 0) ? 6 : dow - 1;
    return { from: dayStart(daysAgo(mondayOffset)), until: nowISO() };
  }
  if (q.indexOf("last month") !== -1) {
    return { from: dayStart(daysAgo(30)), until: nowISO() };
  }
  if (q.indexOf("this month") !== -1) {
    return { from: dayStart(new Date(y, mo, 1)), until: nowISO() };
  }
  var months = ["january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"];
  var monthAbbr = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
  for (var mi = 0; mi < 12; mi++) {
    if (q.indexOf("in " + months[mi]) !== -1 || q.indexOf("in " + monthAbbr[mi]) !== -1) {
      var mYear = (mi > mo) ? y - 1 : y;
      return { from: dayStart(new Date(mYear, mi, 1)), until: dayEnd(new Date(mYear, mi + 1, 0)) };
    }
  }
  return null;
}

function _isoToUnix(iso) {
  var ms = Date.parse(iso);
  return isNaN(ms) ? 0 : Math.floor(ms / 1000);
}

// ============================================================================
// Module-level tool surface — what the agent calls from run_cell. Bound as
// the `convmemory` kernel global. Uses a lazy internal noTrace client (same
// pattern as amemory@v2's _getToolAmem).
// ============================================================================

var _toolConv = null;

function _getToolConv() {
  if (_toolConv) return _toolConv;
  var client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID,
    noTrace: true
  });
  _toolConv = createConvMemory(client);
  return _toolConv;
}

// Resolve the chat object id for history calls: explicit opts.chatId wins,
// else the invocation args' chatId (cells get `args` injected by the kernel).
function _resolveChatId(opts) {
  if (opts && opts.chatId) return opts.chatId;
  try {
    if (typeof args !== "undefined" && args && args.chatId) return args.chatId;
  } catch (e) {}
  try {
    if (globalThis.args && globalThis.args.chatId) return globalThis.args.chatId;
  } catch (e) {}
  return null;
}

function _requireChatId(opts, method) {
  var chatId = _resolveChatId(opts);
  if (!chatId) {
    throw new Error("convmemory." + method + ": no chatId — pass {chatId: args.chatId} (this run has no chat scope)");
  }
  return chatId;
}

// Accept ISO strings or unix seconds for period bounds.
function _toUnix(v) {
  if (typeof v === "number") return v;
  if (typeof v === "string") return _isoToUnix(v);
  return 0;
}

function _recentTurnsImpl(opts) {
  opts = opts || {};
  var chatId = _requireChatId(opts, "recentTurns");
  return { ok: true, turns: _getToolConv().recentTurns(chatId, opts.limit || 8) };
}

function _turnRangeImpl(opts) {
  opts = opts || {};
  if (opts.fromSeq === undefined || opts.toSeq === undefined) {
    return { ok: false, error: "fromSeq and toSeq are required" };
  }
  var chatId = _requireChatId(opts, "turnRange");
  return { ok: true, turns: _getToolConv().turnRange(chatId, opts.fromSeq, opts.toSeq) };
}

function _expandChunkImpl(chunkSeq, opts) {
  if (chunkSeq === undefined || chunkSeq === null) {
    return { ok: false, error: "chunkSeq is required (the [chunk #N] handle from the compressed-context block)" };
  }
  var chatId = _requireChatId(opts, "expandChunk");
  return _getToolConv().expandChunk(chatId, typeof chunkSeq === "string" ? parseInt(chunkSeq, 10) : chunkSeq);
}

function _listChunksImpl(opts) {
  opts = opts || {};
  var chatId = _requireChatId(opts, "listChunks");
  return { ok: true, chunks: _getToolConv().listChunks(chatId, opts) };
}

function _turnsByPeriodImpl(opts) {
  opts = opts || {};
  if (!opts.from || !opts.until) return { ok: false, error: "from and until are required (ISO strings or unix seconds)" };
  var chatId = _requireChatId(opts, "turnsByPeriod");
  return { ok: true, turns: _getToolConv().turnsByPeriod(chatId, _toUnix(opts.from), _toUnix(opts.until)) };
}

function _addMemoryImpl(text, opts) {
  opts = opts || {};
  if (!opts.chatId) {
    var chatId = _resolveChatId(opts);
    if (chatId) opts.chatId = chatId; // provenance, optional
  }
  return _getToolConv().addMemory(text, opts);
}

function _evolveMemoryImpl(itemId, fields) {
  if (!itemId) return { ok: false, error: "itemId is required" };
  return _getToolConv().evolveMemory(itemId, fields);
}

function _memoryByCategoryImpl(opts) {
  opts = opts || {};
  if (!opts.categories || opts.categories.length === 0) return { ok: false, error: "categories is required (string[])" };
  return { ok: true, items: _getToolConv().byCategory(opts.categories, opts.limit || 20) };
}

function _memoryByPeriodImpl(opts) {
  opts = opts || {};
  if (!opts.from || !opts.until) return { ok: false, error: "from and until are required (ISO strings or unix seconds)" };
  return { ok: true, items: _getToolConv().byPeriod(_toUnix(opts.from), _toUnix(opts.until), opts.categories, opts.limit || 20) };
}

function _recentMemoriesImpl(opts) {
  opts = opts || {};
  return { ok: true, items: _getToolConv().recent(opts.limit || 20, opts.categories) };
}

function _listCategoriesImpl() {
  return _getToolConv().listCategories();
}

// search(query, opts) — THE EXTERNAL SEARCH SEAM.
//
// Interface the future vector service must satisfy:
//   globalThis.__searchService.query({ text, chatId?, categories?, k })
//     -> [{ id, score, kind }]   // kind ∈ item|chunk|turn; id = record id
// Hits are hydrated via the indexed id queries below.
//
// Until that service is injected, this falls back to NON-SEMANTIC recall:
//   1. explicit {periodFrom, periodUntil} or a detected temporal phrase
//      ("yesterday", "last week", "in march") → indexed validFrom range
//   2. {categories} → indexed category filter
//   3. otherwise → recency
// Every fallback response carries mode + note so the agent knows similarity
// ranking did NOT happen.
function _searchImpl(query, opts) {
  opts = opts || {};
  var conv = _getToolConv();

  var svc = null;
  try { svc = globalThis.__searchService; } catch (e) {}
  if (svc && typeof svc.query === "function") {
    var hits = svc.query({ text: query || "", chatId: _resolveChatId(opts), categories: opts.categories, k: opts.k || 8 });
    return { ok: true, mode: "semantic", results: hits };
  }

  var k = opts.k || 8;

  // 1. period (explicit or detected)
  var from = opts.periodFrom, until = opts.periodUntil, detected = null;
  if (!from || !until) {
    detected = detectTemporalReference(query || "");
    if (detected) { from = detected.from; until = detected.until; }
  }
  if (from && until) {
    return {
      ok: true,
      mode: detected ? "period_detected" : "period",
      note: "semantic recall unavailable until the external search service lands — this is an indexed period slice, not similarity-ranked",
      detected: detected || undefined,
      results: conv.byPeriod(_toUnix(from), _toUnix(until), opts.categories, k)
    };
  }

  // 2. category filter
  if (opts.categories && opts.categories.length > 0) {
    return {
      ok: true,
      mode: "category",
      note: "semantic recall unavailable until the external search service lands — newest items in the requested categories",
      results: conv.byCategory(opts.categories, k)
    };
  }

  // 3. recency
  return {
    ok: true,
    mode: "recent",
    note: "semantic recall unavailable until the external search service lands — newest items only; narrow with {categories} or a period",
    results: conv.recent(k)
  };
}

// __wrapTrace each public method so the agent-visible trace shows one clean
// "convmemory.X(args) → result" entry per call (same pattern as anyHelper).
var _w = (typeof __wrapTrace === "function")
  ? function(name, fn) { return __wrapTrace("convmemory." + name, fn); }
  : function(_name, fn) { return fn; };

export var recentTurns = _w("recentTurns", _recentTurnsImpl);
export var turnRange = _w("turnRange", _turnRangeImpl);
export var expandChunk = _w("expandChunk", _expandChunkImpl);
export var listChunks = _w("listChunks", _listChunksImpl);
export var turnsByPeriod = _w("turnsByPeriod", _turnsByPeriodImpl);
export var search = _w("search", _searchImpl);
export var addMemory = _w("addMemory", _addMemoryImpl);
export var evolveMemory = _w("evolveMemory", _evolveMemoryImpl);
export var memoryByCategory = _w("memoryByCategory", _memoryByCategoryImpl);
export var memoryByPeriod = _w("memoryByPeriod", _memoryByPeriodImpl);
export var recentMemories = _w("recentMemories", _recentMemoriesImpl);
export var listCategories = _w("listCategories", _listCategoriesImpl);

// Drop convmemory-internal plumbing from the agent-visible trace: the wrapped
// "convmemory.X" entries already capture input → output; the underlying
// fetches to the any API host are noise.
export function __prepareTraces(traces) {
  if (!traces) return traces;
  var apiHost = env.ANYTYPE_API_URL || "";
  var out = {};
  for (var key in traces) {
    if (!Object.prototype.hasOwnProperty.call(traces, key)) continue;
    if (key === "fetch" && apiHost) {
      // Keep only fetch entries NOT aimed at the any API host.
      var rec = traces[key];
      var kept = {};
      var keptAny = false;
      for (var input in rec) {
        if (!Object.prototype.hasOwnProperty.call(rec, input)) continue;
        if (input.indexOf(apiHost) !== -1) continue;
        kept[input] = rec[input];
        keptAny = true;
      }
      if (keptAny) out[key] = kept;
      continue;
    }
    out[key] = traces[key];
  }
  return out;
}

// Standalone smoke entry — run directly via the agent runtime:
//   any-agent-runtime -e .env programs/convmemory@v1.js chatId=<chat object id>
export function main(runArgs) {
  runArgs = runArgs || {};
  var conv = _getToolConv();
  var out = { brainId: conv.brainId(), categories: conv.listCategories() };
  if (runArgs.chatId) {
    out.lastSeq = conv.lastSeq(runArgs.chatId);
    out.chunks = conv.listChunks(runArgs.chatId, { limit: 3 });
  }
  return JSON.stringify(out, null, 2);
}
