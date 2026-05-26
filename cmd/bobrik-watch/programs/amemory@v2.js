// __main_source
import { createClient, getNumber, getProp, getTagKeys } from "anyHelper@v1";
import { createLLM } from "llm@v1";
var llm = createLLM();

// ── Vector encoding (uint8 hex) ──────────────────────────────────────────────
// Encodes float arrays ([-1,1]) as compact hex strings: 2 hex chars per dimension.
// 1536-dim vector → 3072-char string (~3KB) vs ~18KB for JSON.

export function encodeVector(vec) {
  if (!vec || vec.length === 0) return "";
  var hex = "";
  for (var i = 0; i < vec.length; i++) {
    var b = Math.round((vec[i] + 1) * 127.5);
    if (b < 0) b = 0;
    if (b > 255) b = 255;
    hex += (b < 16 ? "0" : "") + b.toString(16);
  }
  return hex;
}

// ── Cosine similarity on hex strings ─────────────────────────────────────────
// Uses native Go cosineSimilarityHex (registered by runtime). All vectors are
// stored and compared as hex — no decoding to float arrays needed.

export function cosineSimilarity(a, b) {
  if (!a || !b) return 0;
  return cosineSimilarityHex(a, b);
}

// ── Anytype FTS (Tantivy BM25) integration ───────────────────────────────────
// Uses Anytype's built-in search API (backed by Tantivy) for keyword matching.
// Returns a normalized rank score: rank 0 → 1.0, rank N-1 → near 0.

function ftsSearch(client, query, typeKey, limit) {
  limit = limit || 20;
  try {
    // Anytype search uses AND for multi-word queries, so we search
    // individual significant terms and merge scores. Each term search
    // gets a rank-based score, then we sum across terms per object.
    var terms = query.toLowerCase().replace(/[^a-z0-9\s]/g, " ").split(/\s+/).filter(function(t) { return t.length > 2; });
    if (terms.length === 0) return {};

    // Deduplicate terms
    var seen = {};
    var uniqueTerms = [];
    for (var i = 0; i < terms.length; i++) {
      if (!seen[terms[i]]) {
        seen[terms[i]] = true;
        uniqueTerms.push(terms[i]);
      }
    }

    var scores = {};
    var maxScore = 0;

    for (var ti = 0; ti < uniqueTerms.length; ti++) {
      var searchQuery = { query: uniqueTerms[ti] };
      if (typeKey) searchQuery.types = [typeKey];
      var results = client.search(searchQuery);
      var n = Math.min(results.length, limit);
      for (var ri = 0; ri < n; ri++) {
        // Rank-based score per term: top result = 1.0, decays linearly
        var rankScore = n > 1 ? (n - 1 - ri) / (n - 1) : 1.0;
        scores[results[ri].id] = (scores[results[ri].id] || 0) + rankScore;
        if (scores[results[ri].id] > maxScore) maxScore = scores[results[ri].id];
      }
    }

    // Normalize to [0, 1]
    if (maxScore > 0) {
      var ids = Object.keys(scores);
      for (var i = 0; i < ids.length; i++) {
        scores[ids[i]] = scores[ids[i]] / maxScore;
      }
    }

    return scores;
  } catch (e) {
    return {};
  }
}

// ── Embedding via openai@v1 ─────────────────────────────────────────────────

var _lastEmbedError = "";

export function getEmbedding(text) {
  _lastEmbedError = "";
  try {
    var result = llm.embed(text);
    if (!result) {
      _lastEmbedError = "llm.embed() returned null/empty";
    }
    return result;
  } catch (e) {
    _lastEmbedError = (e && e.message) ? e.message : String(e);
    return null;
  }
}

export function getLastEmbedError() {
  return _lastEmbedError;
}

// ── Internal: LLM-based metadata extraction (Ps1) ──────────────────────────

// Valid memory categories (matching tag suffixes without the amemory_ prefix)
// "chat_chunk" is a first-class category for compressed chat-history memories;
// agents may ALSO invent new categories via addMemory's skip-classifier branch
// (see _addMemoryDirect below), so this list is "preferred/builtin" rather than
// an exhaustive allow-list at read time. loadAllMemories accepts any
// amemory_<category> tag as a category, minus the two system tags.
export var MEMORY_CATEGORIES = ["claim", "episode", "lesson", "preference", "decision", "taskstate", "insight", "chat_chunk"];

// Step 3: Category-dependent decay rates (per day)
// Episodes fade fast (~100 days to floor), decisions persist longest
var DECAY_RATES = {
  claim: 0.005, episode: 0.01, lesson: 0.002, preference: 0.005,
  decision: 0.001, taskstate: 0.003, insight: 0.002
};
var DEFAULT_DECAY_RATE = 0.005; // for uncategorized/legacy memories

// Step 3: Detect temporal references in query text (regex-based, no LLM)
// Returns {from, until} ISO date strings, or null if no temporal reference found
// FOLLOWUP: query rewriting subprompt for implicit temporal/intent resolution
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
  function nowISO() {
    return now.toISOString().substring(0, 19);
  }
  function daysAgo(n) {
    return new Date(now.getTime() - n * 24 * 60 * 60 * 1000);
  }

  // "yesterday"
  if (q.indexOf("yesterday") !== -1) {
    var yd = daysAgo(1);
    return { from: dayStart(yd), until: dayEnd(yd) };
  }

  // "today" / "so far today"
  if (q.indexOf("today") !== -1) {
    return { from: dayStart(now), until: nowISO() };
  }

  // "N days ago"
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

  // "last week"
  if (q.indexOf("last week") !== -1) {
    return { from: dayStart(daysAgo(7)), until: nowISO() };
  }

  // "this week"
  if (q.indexOf("this week") !== -1) {
    var dow = now.getDay();
    var mondayOffset = (dow === 0) ? 6 : dow - 1;
    var monday = daysAgo(mondayOffset);
    return { from: dayStart(monday), until: nowISO() };
  }

  // "last month"
  if (q.indexOf("last month") !== -1) {
    return { from: dayStart(daysAgo(30)), until: nowISO() };
  }

  // "this month"
  if (q.indexOf("this month") !== -1) {
    var monthStart = new Date(y, mo, 1);
    return { from: dayStart(monthStart), until: nowISO() };
  }

  // Month names: "in january", "in feb", etc.
  var months = ["january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"];
  var monthAbbr = ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"];
  for (var mi = 0; mi < 12; mi++) {
    if (q.indexOf("in " + months[mi]) !== -1 || q.indexOf("in " + monthAbbr[mi]) !== -1) {
      var mYear = (mi > mo) ? y - 1 : y;
      var mStart = new Date(mYear, mi, 1);
      var mEnd = new Date(mYear, mi + 1, 0);
      return { from: dayStart(mStart), until: dayEnd(mEnd) };
    }
  }

  return null;
}

// Step 3: Compute temporal modifier for a memory in search scoring
// Returns a multiplier in [0.1, 1.0] based on age decay,
// with boost to 1.0 for memories inside an explicit temporal window
function computeTemporalModifier(mem, temporalWindow, nowMs) {
  var refDate = mem.validFrom || mem.createdDate || "";
  if (!refDate) return 1.0; // no date info — don't penalize

  // If temporal window exists and memory falls inside it, boost to 1.0
  if (temporalWindow && refDate >= temporalWindow.from && refDate <= temporalWindow.until) {
    return 1.0;
  }

  // Compute days since memory creation/valid_from
  var memMs;
  try {
    memMs = new Date(refDate).getTime();
  } catch (e) {
    return 1.0; // unparseable date — don't penalize
  }
  if (isNaN(memMs)) return 1.0;

  var daysSince = (nowMs - memMs) / (24 * 60 * 60 * 1000);
  if (daysSince < 0) daysSince = 0;

  // Category-dependent decay rate
  var decayRate = DECAY_RATES[mem.category] || DEFAULT_DECAY_RATE;
  var temporal = 1.0 - daysSince * decayRate;
  if (temporal < 0.1) temporal = 0.1;

  return temporal;
}

// TODO: move Levenshtein fuzzy matching to anytype-heart FTS engine (task created). Temporary bridge in JS.
function editDistance(a, b) {
  if (a === b) return 0;
  if (a.length === 0) return b.length;
  if (b.length === 0) return a.length;
  var prev = [];
  var curr = [];
  var i, j;
  for (j = 0; j <= b.length; j++) prev[j] = j;
  for (i = 1; i <= a.length; i++) {
    curr[0] = i;
    for (j = 1; j <= b.length; j++) {
      var cost = (a.charAt(i - 1) === b.charAt(j - 1)) ? 0 : 1;
      var del = prev[j] + 1;
      var ins = curr[j - 1] + 1;
      var sub = prev[j - 1] + cost;
      curr[j] = del < ins ? (del < sub ? del : sub) : (ins < sub ? ins : sub);
    }
    var tmp = prev;
    prev = curr;
    curr = tmp;
  }
  return prev[b.length];
}

function buildMetadataPrompt(content) {
  return "Analyze this content and return JSON with exactly these fields:\n"
    + '{"keywords": ["word1", "word2", ...], "context": "one sentence summary", "tags": ["category1", ...], '
    + '"category": "claim", "entities": ["entity1", ...], "confidence": 8, "importance": 7}\n'
    + "Rules:\n"
    + "- keywords: 3-7 distinct concepts, nouns/verbs, ordered by importance\n"
    + "- context: 1-2 sentences covering topic, key points, purpose\n"
    + "- tags: 2-4 broad categories for classification\n"
    + "- category: exactly ONE of: claim, episode, lesson, preference, decision, taskstate, insight\n"
    + "  claim=factual statement, episode=event/experience, lesson=learned rule/warning,\n"
    + "  preference=user preference/like/dislike, decision=choice made,\n"
    + "  taskstate=task/project status, insight=synthesized understanding\n"
    + "- entities: 1-5 canonical entity names (people, tools, projects, concepts) mentioned in the content, lowercase\n"
    + "- confidence: 0-10, how certain is this information (10=verified fact, 0=wild guess)\n"
    + "- importance: 1-10, how important is this to remember long-term (10=critical, 1=trivial)\n"
    + "Return ONLY the JSON object, no markdown or extra text.\n\n"
    + "Content:\n" + content;
}

function parseMetadataResponse(resp) {
  var parsed = llm.parseJSON(resp);
  if (!parsed) return null;
  try {
    // Validate category — must be one of the known types, default to "claim"
    var category = parsed.category || "";
    if (MEMORY_CATEGORIES.indexOf(category) === -1) {
      category = "claim";
    }

    // Validate entities — must be an array of strings
    var entities = [];
    if (Array.isArray(parsed.entities)) {
      for (var i = 0; i < parsed.entities.length; i++) {
        if (typeof parsed.entities[i] === "string" && parsed.entities[i].length > 0) {
          entities.push(parsed.entities[i].toLowerCase());
        }
      }
    }

    // Validate confidence (0-10) and importance (1-10)
    var confidence = typeof parsed.confidence === "number" ? parsed.confidence : 5;
    if (confidence < 0) confidence = 0;
    if (confidence > 10) confidence = 10;

    var importance = typeof parsed.importance === "number" ? parsed.importance : 5;
    if (importance < 1) importance = 1;
    if (importance > 10) importance = 10;

    return {
      keywords: parsed.keywords || [],
      context: parsed.context || "",
      tags: parsed.tags || [],
      category: category,
      entities: entities,
      confidence: Math.round(confidence),
      importance: Math.round(importance)
    };
  } catch (e) {
    return null;
  }
}

// ── Internal: Link generation (Ps2) ────────────────────────────────────────

function generateLinks(newMemory, candidates) {
  if (!candidates || candidates.length === 0) return [];

  var candidateList = "";
  for (var i = 0; i < candidates.length; i++) {
    var c = candidates[i];
    candidateList += "- ID: " + c.id + ", context: " + c.context + ", keywords: " + c.keywords + ", similarity: " + c.similarity.toFixed(3) + "\n";
  }

  var prompt = "You analyze memory connections. Given a new memory and candidate related memories,\n"
    + "decide which should be linked and classify the relationship type.\n\n"
    + "New memory:\n"
    + "Content: " + newMemory.content + "\n"
    + "Context: " + newMemory.context + "\n"
    + "Keywords: " + newMemory.keywords + "\n\n"
    + "Candidates:\n" + candidateList + "\n"
    + 'Return JSON: {"links": [{"id": "id1", "type": "related_to"}, {"id": "id2", "type": "caused_by"}]}\n'
    + "Valid edge types: related_to, caused_by, leads_to, contradicts, supersedes, supported_by\n"
    + "- related_to: general topical connection\n"
    + "- caused_by: new memory is caused by/consequence of the candidate\n"
    + "- leads_to: new memory leads to/enables the candidate\n"
    + "- contradicts: new memory conflicts with the candidate\n"
    + "- supersedes: new memory replaces/updates the candidate\n"
    + "- supported_by: new memory is evidence for the candidate\n"
    + "Include only candidates that should be linked. If none, return {\"links\": []}.\n"
    + "Return ONLY the JSON object, no markdown or extra text.";

  var parsed = llm.parseJSON(llm.reason(prompt));
  if (!parsed || !parsed.links) return [];

  // Normalize: handle both old format (array of strings) and new format (array of objects)
  var links = parsed.links;
  var normalized = [];
  for (var i = 0; i < links.length; i++) {
    if (typeof links[i] === "string") {
      // Old format: plain ID string — default to related_to
      normalized.push({ id: links[i], type: "related_to" });
    } else if (links[i] && links[i].id) {
      var edgeType = links[i].type || "related_to";
      var validTypes = ["related_to", "caused_by", "leads_to", "contradicts", "supersedes", "supported_by"];
      if (validTypes.indexOf(edgeType) === -1) edgeType = "related_to";
      normalized.push({ id: links[i].id, type: edgeType });
    }
  }
  return normalized;
}

// Reverse edge type for bidirectional edge storage
function reverseEdgeType(edgeType) {
  if (edgeType === "caused_by") return "leads_to";
  if (edgeType === "leads_to") return "caused_by";
  // contradicts, related_to, supersedes, supported_by — symmetric or same
  return edgeType;
}

// ── Internal: Memory evolution (Ps3) ────────────────────────────────────────

// Build evolution prompt (without calling LLM) — used by both single and batch paths
function buildEvolvePrompt(newMemory, existingMemory, similarity) {
  var simNote = "";
  if (similarity !== undefined && similarity > 0.4) {
    simNote = "\nIMPORTANT: These memories have high cosine similarity (" + similarity.toFixed(3)
      + "). The existing memory's context and keywords should be enriched to reflect "
      + "the combined knowledge from both memories. Prefer evolving when similarity > 0.4.\n";
  }

  return "You are a memory evolution agent. When a new memory is closely related to an existing one,\n"
    + "the existing memory should evolve — its context should broaden to reflect the combined knowledge,\n"
    + "keywords should be enriched with new concepts, and tags updated if new categories emerge.\n"
    + simNote + "\n"
    + "New memory:\n"
    + "Content: " + newMemory.content + "\n"
    + "Context: " + newMemory.context + "\n"
    + "Keywords: " + newMemory.keywords + "\n\n"
    + "Existing memory:\n"
    + "Content: " + existingMemory.content + "\n"
    + "Context: " + existingMemory.context + "\n"
    + "Keywords: " + existingMemory.keywords + "\n"
    + "Tags: " + existingMemory.tags + "\n\n"
    + 'Return JSON: {"should_evolve": bool, "new_context": "...", "new_keywords": "...", "new_tags": ["..."]}\n'
    + "Evolve the memory if the new memory adds information, nuance, or connections not already captured.\n"
    + "Return ONLY the JSON object, no markdown or extra text.";
}

function parseEvolveResponse(resp) {
  return llm.parseJSON(resp);
}

function evolveMemory(newMemory, existingMemory, similarity) {
  var prompt = buildEvolvePrompt(newMemory, existingMemory, similarity);
  return parseEvolveResponse(llm.reason(prompt));
}

// ── Internal: Cluster summarization ─────────────────────────────────────────

function summarizeCluster(tag, memberTexts, query) {
  var prompt = "Summarize these related memories into a single concise paragraph.\n"
    + "Focus on information relevant to the query: " + query + "\n"
    + "Combine overlapping facts, remove redundancy, keep unique details.\n"
    + "Category: " + tag + "\n\n"
    + "Memories:\n" + memberTexts + "\n"
    + "Return ONLY the summary paragraph, no markdown headers or extra formatting.";

  var resp = llm.summarize(prompt);
  if (!resp) return memberTexts;
  return resp.trim();
}

// ── Ps4: Verify — quality gate for individual memories ──────────────────────

function verifyMemoryData(mem, totalMemCount) {
  var issues = [];
  var score = 5;

  // Check 1: Has vector embedding?
  if (!mem.vec || mem.vec.length === 0) {
    issues.push({ severity: "FAIL", check: "vector", msg: "Missing embedding vector — memory is unsearchable" });
    score -= 2;
  }

  // Check 2: Context quality — not empty, not just repeating the name
  var ctx = mem.context || "";
  if (ctx.length < 10) {
    issues.push({ severity: "FAIL", check: "context", msg: "Context too short (" + ctx.length + " chars) — needs meaningful summary" });
    score -= 1;
  } else if (mem.name && ctx.toLowerCase() === mem.name.toLowerCase()) {
    issues.push({ severity: "WARN", check: "context", msg: "Context just repeats the title — should describe mechanism/insight" });
    score -= 0.5;
  }

  // Check 3: Keywords present and sufficient
  var kw = mem.keywords || "";
  var kwCount = kw ? kw.split(",").length : 0;
  if (kwCount < 2) {
    issues.push({ severity: "WARN", check: "keywords", msg: "Only " + kwCount + " keyword(s) — aim for 3-7" });
    score -= 0.5;
  }

  // Check 4: Has tags
  var tagCount = mem.tags ? mem.tags.length : 0;
  if (tagCount === 0) {
    issues.push({ severity: "WARN", check: "tags", msg: "No tags — memory won't appear in category filters" });
    score -= 0.5;
  }

  // Check 5: Link integration (only enforce when graph is large enough)
  if (totalMemCount > 10) {
    var hasLinks = false;
    if (mem.body && mem.body.indexOf("## Links") !== -1) {
      hasLinks = true;
    }
    if (!hasLinks) {
      issues.push({ severity: "WARN", check: "links", msg: "No outgoing links — memory is an orphan in the graph" });
      score -= 0.5;
    }
  }

  if (score < 0) score = 0;

  var status = "PASS";
  for (var i = 0; i < issues.length; i++) {
    if (issues[i].severity === "FAIL") { status = "FAIL"; break; }
    if (issues[i].severity === "WARN") status = "WARN";
  }

  return { status: status, score: Math.round(score * 10) / 10, issues: issues };
}

// ── Ps5: Rethink — memory graph health audit ────────────────────────────────

function findStaleMemories(allMems) {
  // Stale = no links AND no backlinks AND no tags
  var stale = [];
  for (var i = 0; i < allMems.length; i++) {
    var m = allMems[i];
    var hasLinks = m.body && m.body.indexOf("## Links") !== -1;
    var hasTags = m.tags && m.tags.length > 0;
    if (!hasLinks && !hasTags) {
      stale.push({ id: m.id, name: m.name, context: m.context });
    }
  }
  return stale;
}

function findDuplicateCandidates(allMems, threshold) {
  threshold = threshold || 0.55;
  var dupes = [];
  for (var i = 0; i < allMems.length; i++) {
    for (var j = i + 1; j < allMems.length; j++) {
      // Use content-only embeddings for dedup (not enriched) —
      // enriched embeddings spread near-duplicates apart because different
      // LLM-generated metadata gets mixed in. Content-only measures
      // actual information overlap.
      // Prefer hex path (native Go, no JS array marshaling) when available.
      var hexA = allMems[i].contentVec || allMems[i].vec;
      var hexB = allMems[j].contentVec || allMems[j].vec;
      if (!hexA || !hexB) continue;
      var sim = cosineSimilarity(hexA, hexB);
      if (sim >= threshold) {
        dupes.push({
          memA: { id: allMems[i].id, name: allMems[i].name, context: allMems[i].context },
          memB: { id: allMems[j].id, name: allMems[j].name, context: allMems[j].context },
          similarity: Math.round(sim * 1000) / 1000
        });
      }
    }
  }
  // Sort by similarity descending
  dupes.sort(function(a, b) { return b.similarity - a.similarity; });
  return dupes;
}

function findWeakMemories(allMems) {
  // Weak = has vector but low metadata quality
  var weak = [];
  for (var i = 0; i < allMems.length; i++) {
    var m = allMems[i];
    var kw = m.keywords || "";
    var kwCount = kw ? kw.split(",").length : 0;
    var ctxLen = (m.context || "").length;
    // Weak if context < 20 chars OR less than 2 keywords
    if (ctxLen < 20 || kwCount < 2) {
      weak.push({
        id: m.id,
        name: m.name,
        context: m.context,
        reason: ctxLen < 20 ? "short context (" + ctxLen + " chars)" : "few keywords (" + kwCount + ")"
      });
    }
  }
  return weak;
}

// ── Internal: Load all A-mem objects ────────────────────────────────────────

function stripTagPrefix(tags) {
  var result = [];
  for (var i = 0; i < tags.length; i++) {
    var t = tags[i];
    if (t.indexOf("amemory_") === 0) {
      result.push(t.substring(8));
    }
  }
  return result;
}

// Build a category filter predicate from opts.categories (whitelist) and
// opts.excludeCategories (blacklist). Returns null when no filter is set, so
// callers can skip the filter step entirely without branching on empties.
// Applied absolutely: filter is honoured for initial retrieval AND link
// expansion, so {categories: ["claim"]} means claims only, even when a claim
// links to a lesson.
function _buildCategoryFilter(whitelist, blacklist) {
  var hasWL = Array.isArray(whitelist) && whitelist.length > 0;
  var hasBL = Array.isArray(blacklist) && blacklist.length > 0;
  if (!hasWL && !hasBL) return null;
  var wlSet = {};
  if (hasWL) for (var i = 0; i < whitelist.length; i++) wlSet[whitelist[i]] = true;
  var blSet = {};
  if (hasBL) for (var j = 0; j < blacklist.length; j++) blSet[blacklist[j]] = true;
  return function(m) {
    var c = m && m.category;
    if (hasWL && !wlSet[c]) return false;
    if (hasBL && blSet[c]) return false;
    return true;
  };
}

// Build a chatId filter for chat-chunk memories. `mode` is one of:
//   "all"     — no filter (also returned when chatId is empty)
//   "only"    — chunks whose chatId equals the given id; non-chunks pass through
//   "exclude" — chunks whose chatId differs from the given id; non-chunks pass
// Non-chunk memories are never filtered here — chat scoping only applies to
// chat_chunk records; other categories stay space-global.
function _buildChatIdFilter(chatId, mode) {
  mode = mode || "all";
  if (mode === "all" || !chatId) return null;
  return function(m) {
    if (!m || !m.tags || m.tags.indexOf("chat_chunk") === -1) return true;
    if (mode === "only")    return m.chatId === chatId;
    if (mode === "exclude") return m.chatId !== chatId;
    return true;
  };
}

// Detect the category of a memory from its raw (prefixed) tag list.
// chat_chunk takes priority when present — legacy data written before chunks
// were a first-class category carries BOTH amemory_episode and amemory_chat_chunk
// tags, and the truer label is chunk. Otherwise the first amemory_<cat> tag
// wins, skipping system tags (boot_state, archived) that carry no category.
function _detectCategory(rawTags) {
  if (!rawTags) return "";
  for (var i = 0; i < rawTags.length; i++) {
    if (rawTags[i] === "amemory_chat_chunk") return "chat_chunk";
  }
  for (var j = 0; j < rawTags.length; j++) {
    var t = rawTags[j];
    if (!t || t.indexOf("amemory_") !== 0) continue;
    var cat = t.substring(8);
    if (!cat) continue;
    if (cat === "boot_state" || cat === "archived") continue;
    return cat;
  }
  return "";
}

function loadAllMemories(client, typeKey) {
  var objects = client.getObjects(typeKey);
  var memories = [];
  for (var i = 0; i < objects.length; i++) {
    var obj = objects[i];
    var vectorHex = obj.__amemory_vector;
    if (!vectorHex) continue;

    // Step 7/9: Skip boot state and archived memories (they're not real memories)
    var objTags = getTagKeys(obj, "tag") || [];
    var skipObj = false;
    for (var sti = 0; sti < objTags.length; sti++) {
      if (objTags[sti] === "amemory_boot_state" || objTags[sti] === "amemory_archived") {
        skipObj = true;
        break;
      }
    }
    if (skipObj) continue;

    // Extract dates for context display (not scored — LLM judges temporal relevance)
    var createdDate = obj.created_date || "";
    var modDate = obj.last_modified_date || "";

    // Step 1: Load structured properties (with backward-compatible defaults for old memories)
    var confidence = getNumber(obj, "__amemory_confidence");
    var importance = getNumber(obj, "__amemory_importance");
    var salience = getNumber(obj, "__amemory_salience");
    var accessCount = getNumber(obj, "__amemory_access_count");
    var validFrom = obj.__amemory_valid_from || "";
    var entities = obj.__amemory_entities || "";
    var edges = obj.__amemory_edges || "[]";
    var periodStart = obj.__amemory_period_start || "";
    var periodEnd = obj.__amemory_period_end || "";
    var chatId = obj.__amemory_chat_id || "";

    // Determine category from tags. chat_chunk wins if present (legacy data has
    // both amemory_episode + amemory_chat_chunk — chat_chunk is the truer label).
    // Fall back to any amemory_<cat> tag, skipping system tags.
    var allTags = objTags;
    var category = _detectCategory(allTags);

    memories.push({
      id: obj.id,
      name: obj.name,
      context: obj.__amemory_context || "",
      keywords: obj.__amemory_keywords || "",
      tags: stripTagPrefix(allTags),
      vec: vectorHex,
      createdDate: createdDate,
      modDate: modDate,
      // Step 1: Structured fields
      category: category || "claim",  // default for old memories
      confidence: confidence !== null ? confidence : 5,
      importance: importance !== null ? importance : 5,
      salience: salience !== null ? salience : 10,
      accessCount: accessCount !== null ? accessCount : 0,
      validFrom: validFrom || createdDate || "",
      entities: entities,
      edges: edges,
      periodStart: periodStart,
      periodEnd: periodEnd,
      chatId: chatId,
      obj: obj
    });
  }
  return memories;
}

// Extended loader that also fetches body (for verify/rethink — heavier)
function loadAllMemoriesFull(client, typeKey) {
  var objects = client.getObjects(typeKey);
  var memories = [];
  for (var i = 0; i < objects.length; i++) {
    var obj = objects[i];
    var vectorHex = obj.__amemory_vector || "";
    var contentVecHex = obj.__amemory_vector_content || "";

    // Step 7/9: Skip boot state and archived memories
    var objTags = getTagKeys(obj, "tag") || [];
    var skipObj = false;
    for (var sti = 0; sti < objTags.length; sti++) {
      if (objTags[sti] === "amemory_boot_state" || objTags[sti] === "amemory_archived") {
        skipObj = true;
        break;
      }
    }
    if (skipObj) continue;

    var fullObj = client.getObject(obj.id);
    var body = (fullObj && fullObj.markdown) ? fullObj.markdown : "";

    // Step 1: Load structured properties (with backward-compatible defaults)
    var confidence = getNumber(obj, "__amemory_confidence");
    var importance = getNumber(obj, "__amemory_importance");
    var salience = getNumber(obj, "__amemory_salience");
    var accessCount = getNumber(obj, "__amemory_access_count");
    var validFrom = obj.__amemory_valid_from || "";
    var entities = obj.__amemory_entities || "";
    var edges = obj.__amemory_edges || "[]";

    var allTags = objTags;
    var category = _detectCategory(allTags);

    memories.push({
      id: obj.id,
      name: obj.name,
      context: obj.__amemory_context || "",
      keywords: obj.__amemory_keywords || "",
      tags: stripTagPrefix(allTags),
      vec: vectorHex,
      contentVec: contentVecHex,
      body: body,
      // Step 1: Structured fields
      category: category || "claim",
      confidence: confidence !== null ? confidence : 5,
      importance: importance !== null ? importance : 5,
      salience: salience !== null ? salience : 10,
      accessCount: accessCount !== null ? accessCount : 0,
      validFrom: validFrom,
      entities: entities,
      edges: edges,
      chatId: obj.__amemory_chat_id || "",
      obj: obj
    });
  }
  return memories;
}

// Step 7/9: Load ALL memories including archived (for decay processing).
// Skips boot state only.
function loadAllMemoriesForDecay(client, typeKey) {
  var objects = client.getObjects(typeKey);
  var memories = [];
  for (var i = 0; i < objects.length; i++) {
    var obj = objects[i];

    // Skip boot state objects only (archived memories need decay processing too)
    var objTags = getTagKeys(obj, "tag") || [];
    var isBootState = false;
    for (var sti = 0; sti < objTags.length; sti++) {
      if (objTags[sti] === "amemory_boot_state") {
        isBootState = true;
        break;
      }
    }
    if (isBootState) continue;

    // Check if already archived
    var isArchived = false;
    for (var ati = 0; ati < objTags.length; ati++) {
      if (objTags[ati] === "amemory_archived") {
        isArchived = true;
        break;
      }
    }

    var confidence = getNumber(obj, "__amemory_confidence");
    var importance = getNumber(obj, "__amemory_importance");
    var salience = getNumber(obj, "__amemory_salience");
    var accessCount = getNumber(obj, "__amemory_access_count");
    var validFrom = obj.__amemory_valid_from || "";

    var category = _detectCategory(objTags);

    // Extract createdDate
    var createdDate = obj.created_date || "";

    memories.push({
      id: obj.id,
      name: obj.name || "",
      category: category || "claim",
      confidence: confidence !== null ? confidence : 5,
      importance: importance !== null ? importance : 5,
      salience: salience !== null ? salience : 10,
      accessCount: accessCount !== null ? accessCount : 0,
      validFrom: validFrom || createdDate || "",
      isArchived: isArchived
    });
  }
  return memories;
}

// ── Factory: createAMemory ──────────────────────────────────────────────────

export function createAMemory(client, opts) {
  opts = opts || {};
  var typeKey = opts.typeKey || "at_memory";
  var topK = opts.topK || 3;
  var minSimilarity = opts.minSimilarity !== undefined ? opts.minSimilarity : 0.3;
  var enableEvolution = opts.enableEvolution !== undefined ? opts.enableEvolution : true;
  var enableLinks = opts.enableLinks !== undefined ? opts.enableLinks : true;
  var debugHook = opts.debugHook || null;  // function(event, data) — optional debug callback

  // Ensure the memory type and all required properties exist (bootstrap on empty space)
  try {
    client.createType({ key: typeKey, name: "Agent Memory", plural_name: "Agent Memories", icon: { name: "library", color: "blue" }, properties: [
      { key: "__amemory_context", format: "text" },
      { key: "__amemory_keywords", format: "text" },
      { key: "__amemory_vector", format: "text" },
      { key: "__amemory_vector_content", format: "text" },
      // Step 1: Structured properties for memory graph
      { key: "__amemory_confidence", format: "number" },
      { key: "__amemory_importance", format: "number" },
      { key: "__amemory_salience", format: "number" },
      { key: "__amemory_access_count", format: "number" },
      { key: "__amemory_valid_from", format: "text" },
      { key: "__amemory_entities", format: "text" },
      { key: "__amemory_edges", format: "text" },
      // Chat-chunk period span (set only on chat_chunk-tagged memories)
      { key: "__amemory_period_start", format: "date" },
      { key: "__amemory_period_end", format: "date" },
      // Chat-scoping — set on chat_chunk memories to track which chat the
      // compressed history came from. Empty on space-wide memories.
      { key: "__amemory_chat_id", format: "text" }
    ]});
  } catch (e) {
    // Type may already exist — that's fine
  }
  // FTS gate: when ftsWeight > 0, FTS search is enabled (actual scoring weights come from recallWeights / W)
  var ftsWeight = opts.ftsWeight !== undefined ? opts.ftsWeight : 0.2;
  // Duplicate detection threshold (on content-only embeddings)
  var duplicateThreshold = opts.duplicateThreshold !== undefined ? opts.duplicateThreshold : 0.55;

  // ── _addMemoryDirect — skip-classifier write path ────────────────────
  // Used when the caller (typically the agent) already knows the category and
  // has a one-line summary — the Haiku classifier hop adds no value. Required:
  // memOpts.category (any non-empty string; invented categories are accepted
  // here, unlike the classifier path) and memOpts.context (one-line summary).
  // Optional: entities [], confidence 7, importance 5, tags []. Keywords are
  // derived trivially from the content. Content embedding runs once (no
  // parallel batch because there's no classifier fetch to pair it with).
  function _addMemoryDirect(content, memOpts) {
    var category = memOpts.category;
    var context = memOpts.context;
    var entities = [];
    if (Array.isArray(memOpts.entities)) {
      var entSeen = {};
      for (var ei = 0; ei < memOpts.entities.length; ei++) {
        var e = String(memOpts.entities[ei] || "").toLowerCase().trim();
        if (e && !entSeen[e]) { entSeen[e] = true; entities.push(e); }
      }
    }
    var confidence = typeof memOpts.confidence === "number"
      ? Math.max(0, Math.min(10, Math.round(memOpts.confidence))) : 7;
    var importance = typeof memOpts.importance === "number"
      ? Math.max(1, Math.min(10, Math.round(memOpts.importance))) : 5;
    var extraTags = [];
    if (Array.isArray(memOpts.tags)) {
      for (var tgi = 0; tgi < memOpts.tags.length; tgi++) {
        var tg = memOpts.tags[tgi];
        if (typeof tg === "string" && tg.length > 0 && extraTags.indexOf(tg) === -1) {
          extraTags.push(tg);
        }
      }
    }

    // Derive keywords from content: lowercase, non-alphanumeric → space, split,
    // filter ≥3 chars, dedup, cap at 10. No LLM hop.
    var words = String(content).toLowerCase().replace(/[^a-z0-9\s]/g, " ").split(/\s+/);
    var kwSeen = {};
    var keywords = [];
    for (var wi = 0; wi < words.length && keywords.length < 10; wi++) {
      var w = words[wi];
      if (w.length < 3) continue;
      if (kwSeen[w]) continue;
      kwSeen[w] = true;
      keywords.push(w);
    }

    // Embeddings: content-only (for dedup/semantic content recall) + enriched
    // (content + context + keywords + tags, for query recall).
    var contentEmbedding = null;
    var embedding = null;
    try { contentEmbedding = llm.embed(content); } catch (e) {}
    var embeddingText = content + " " + context + " " + keywords.join(" ") + " " + extraTags.join(" ");
    try { embedding = llm.embed(embeddingText); } catch (e) {}
    var embeddingHex = embedding ? encodeVector(embedding) : "";
    var contentEmbeddingHex = contentEmbedding ? encodeVector(contentEmbedding) : "";

    // Dedup gate: if a same-category memory's content embedding is ≥ threshold
    // similar to the new content, skip the write and return a deduplicated
    // result. Prevents agent-side accidents where a save happens despite the
    // equivalent memory being visible in this turn's search results. Opt out
    // with memOpts.skipDedup: true. Threshold 0.85 catches near-paraphrases
    // while allowing meaningfully-distinct entries in the same category.
    if (!memOpts.skipDedup && contentEmbeddingHex) {
      var dedupThreshold = typeof memOpts.dedupThreshold === "number"
        ? memOpts.dedupThreshold : 0.85;
      var sameCatMems = loadAllMemories(client, typeKey);
      var bestDup = null;
      for (var di = 0; di < sameCatMems.length; di++) {
        var mm = sameCatMems[di];
        if (mm.category !== category) continue;
        var mmContentVec = mm.obj && mm.obj.__amemory_vector_content;
        if (!mmContentVec) continue;
        var sim = cosineSimilarity(contentEmbeddingHex, mmContentVec);
        if (sim >= dedupThreshold && (!bestDup || sim > bestDup.similarity)) {
          bestDup = { id: mm.id, context: mm.context, name: mm.name, similarity: sim };
        }
      }
      if (bestDup) {
        return {
          ok: true,
          deduplicated: true,
          duplicateOf: bestDup.id,
          similarity: bestDup.similarity,
          existingContext: bestDup.context,
          existingName: bestDup.name
        };
      }
    }

    var properties = [];
    properties.push({ key: "__amemory_keywords", text: keywords.join(", ") });
    properties.push({ key: "__amemory_context", text: context });
    if (embeddingHex) properties.push({ key: "__amemory_vector", text: embeddingHex });
    if (contentEmbeddingHex) properties.push({ key: "__amemory_vector_content", text: contentEmbeddingHex });
    properties.push({ key: "__amemory_confidence", number: confidence });
    properties.push({ key: "__amemory_importance", number: importance });
    properties.push({ key: "__amemory_salience", number: 10 });
    properties.push({ key: "__amemory_access_count", number: 0 });
    properties.push({ key: "__amemory_valid_from", text: new Date().toISOString() });
    if (entities.length > 0) properties.push({ key: "__amemory_entities", text: entities.join(", ") });
    properties.push({ key: "__amemory_edges", text: "[]" });

    var result = client.createObject(typeKey, {
      name: context.substring(0, 80),  // display-name cap, pre-existing convention
      body: content,
      properties: properties
    });
    if (!result || !result.ok) {
      return { ok: false, error: (result && result.error) || "createObject failed" };
    }
    var objId = result.object.id;

    try { client.addTag(objId, "amemory"); } catch (e) {}
    try { client.addTag(objId, "amemory_" + category); } catch (e) {}
    for (var eti = 0; eti < extraTags.length; eti++) {
      try { client.addTag(objId, "amemory_" + extraTags[eti]); } catch (e) {}
    }

    return {
      ok: true, id: objId, category: category, context: context,
      keywords: keywords, entities: entities, tags: extraTags, direct: true
    };
  }

  // ── addMemory ─────────────────────────────────────────────────────────

  function addMemory(content, memOpts) {
    memOpts = memOpts || {};
    // Agent-direct path: caller supplied category + a one-line context summary.
    // Skip the Haiku classifier entirely — the caller is already authoritative.
    // Invented categories are accepted here (classifier path keeps its strict
    // MEMORY_CATEGORIES check because the classifier prompt targets that set).
    if (memOpts.category && typeof memOpts.context === "string" && memOpts.context.length > 0) {
      return _addMemoryDirect(content, memOpts);
    }
    // Ps1: Extract metadata + content embedding in parallel via fetchBatch
    // metadata extraction (LLM) and content-only embedding (OpenAI) are independent
    var metaPrompt = buildMetadataPrompt(content);
    var metaFetchArgs = llm.buildCompleteFetchArgs(metaPrompt, "classify");
    var metaProvider = metaFetchArgs[metaFetchArgs.length - 1]; // provider is last element
    var contentEmbedFetchArgs = llm.buildEmbedFetchArgs([content]);
    var parallelResults = fetchBatch([metaFetchArgs, contentEmbedFetchArgs]);

    var meta = parseMetadataResponse(llm.parseCompleteResult(parallelResults[0], metaProvider));
    if (!meta) {
      meta = { keywords: [], context: content.substring(0, 80), tags: [], category: "claim", entities: [], confidence: 5, importance: 5 };
    }

    // Apply overrides from memOpts (pre-classified metadata from extractInsights, etc.)
    if (memOpts.category && MEMORY_CATEGORIES.indexOf(memOpts.category) !== -1) {
      meta.category = memOpts.category;
    }
    if (memOpts.entities && Array.isArray(memOpts.entities) && memOpts.entities.length > 0) {
      // Merge: deduplicate, prefer opts entities first
      var entitySet = {};
      for (var ei = 0; ei < memOpts.entities.length; ei++) {
        var e = memOpts.entities[ei].toLowerCase();
        if (e) entitySet[e] = true;
      }
      for (var ei = 0; ei < meta.entities.length; ei++) {
        entitySet[meta.entities[ei]] = true;
      }
      meta.entities = Object.keys(entitySet);
    }
    if (typeof memOpts.confidence === "number") {
      meta.confidence = Math.max(0, Math.min(10, Math.round(memOpts.confidence)));
    }
    if (typeof memOpts.importance === "number") {
      meta.importance = Math.max(1, Math.min(10, Math.round(memOpts.importance)));
    }
    // Merge extra tags from memOpts (e.g., "codegen_lesson")
    if (memOpts.tags && Array.isArray(memOpts.tags)) {
      for (var tgi = 0; tgi < memOpts.tags.length; tgi++) {
        if (typeof memOpts.tags[tgi] === "string" && meta.tags.indexOf(memOpts.tags[tgi]) === -1) {
          meta.tags.push(memOpts.tags[tgi]);
        }
      }
    }

    var contentEmbedding = null;
    var embedWarning = "";
    try {
      var contentEmbeds = llm.parseEmbedResult(parallelResults[1]);
      contentEmbedding = contentEmbeds[0] || null;
    } catch (e) {
      embedWarning = (e && e.message) ? e.message : String(e);
    }

    // Ps1b: Generate enriched embedding (depends on metadata)
    var embeddingText = content + " " + meta.context + " " + meta.keywords.join(" ") + " " + meta.tags.join(" ");
    var embedding = null;
    try {
      embedding = llm.embed(embeddingText);
    } catch (e) {
      embedWarning = embedWarning || ((e && e.message) ? e.message : String(e));
    }
    if (!embedding) {
      embedWarning = embedWarning || "embedding failed";
    }

    // Encode to hex for storage and comparison
    var embeddingHex = embedding ? encodeVector(embedding) : "";
    var contentEmbeddingHex = contentEmbedding ? encodeVector(contentEmbedding) : "";

    // Create Anytype object
    var properties = [];
    properties.push({ key: "__amemory_keywords", text: meta.keywords.join(", ") });
    properties.push({ key: "__amemory_context", text: meta.context });
    if (embeddingHex) {
      properties.push({ key: "__amemory_vector", text: embeddingHex });
    }
    if (contentEmbeddingHex) {
      properties.push({ key: "__amemory_vector_content", text: contentEmbeddingHex });
    }
    // Step 1: Structured properties
    properties.push({ key: "__amemory_confidence", number: meta.confidence });
    properties.push({ key: "__amemory_importance", number: meta.importance });
    properties.push({ key: "__amemory_salience", number: 10 }); // starts at max
    properties.push({ key: "__amemory_access_count", number: 0 });
    properties.push({ key: "__amemory_valid_from", text: new Date().toISOString() });
    if (meta.entities.length > 0) {
      properties.push({ key: "__amemory_entities", text: meta.entities.join(", ") });
    }
    // Edges start empty — populated by Ps2 link generation in later steps
    properties.push({ key: "__amemory_edges", text: "[]" });

    var result = client.createObject(typeKey, {
      name: meta.context.substring(0, 80),
      body: content,
      properties: properties
    });

    if (!result.ok) {
      return { ok: false, error: result.error };
    }

    var objId = result.object.id;

    // Add base amemory tag (for FTS filtering) + classification tags with amemory_ prefix
    try { client.addTag(objId, "amemory"); } catch (e) {}
    // Add category as prefixed tag (e.g., "preference" → "amemory_preference")
    if (meta.category) {
      try { client.addTag(objId, "amemory_" + meta.category); } catch (e) {}
    }
    for (var ti = 0; ti < meta.tags.length; ti++) {
      try {
        client.addTag(objId, "amemory_" + meta.tags[ti]);
      } catch (e) {}
    }

    // Ps2: Link generation
    if (enableLinks && embeddingHex) {
      try {
        var allMems = loadAllMemories(client, typeKey);
        var candidates = [];
        for (var mi = 0; mi < allMems.length; mi++) {
          if (allMems[mi].id === objId) continue;
          var sim = cosineSimilarity(embeddingHex, allMems[mi].vec);
          candidates.push({
            id: allMems[mi].id,
            name: allMems[mi].name,
            context: allMems[mi].context,
            keywords: allMems[mi].keywords,
            similarity: sim,
            obj: allMems[mi].obj
          });
        }
        candidates.sort(function(a, b) { return b.similarity - a.similarity; });
        // Skip LLM call if no candidates pass similarity threshold
        var topCandidates = candidates.slice(0, 5).filter(function(c) { return c.similarity >= minSimilarity; });

        if (topCandidates.length > 0) {
          // Step 4d: generateLinks now returns [{id, type}] with edge types
          var linkResults = generateLinks(
            { content: content, context: meta.context, keywords: meta.keywords.join(", ") },
            topCandidates
          );

          if (linkResults.length > 0) {
            // Build lookup for candidate similarity (for edge strength)
            var candidateSims = {};
            for (var csi = 0; csi < topCandidates.length; csi++) {
              candidateSims[topCandidates[csi].id] = topCandidates[csi].similarity;
            }

            // Build markdown links section
            var linksSection = "\n\n## Links\n\n";
            for (var li = 0; li < linkResults.length; li++) {
              var linkId = linkResults[li].id;
              var linkName = linkId;
              for (var ci = 0; ci < topCandidates.length; ci++) {
                if (topCandidates[ci].id === linkId) {
                  linkName = topCandidates[ci].name || linkId;
                  break;
                }
              }
              linksSection += "[" + linkName + "](anytype://object?objectId=" + linkId + ")    \n";
            }

            // Step 4d: Build forward edges for the new memory
            var forwardEdges = [];
            for (var fei = 0; fei < linkResults.length; fei++) {
              forwardEdges.push({
                to: linkResults[fei].id,
                type: linkResults[fei].type,
                strength: candidateSims[linkResults[fei].id] || 0.5
              });
            }

            // Update new memory with links markdown AND forward edges
            client.updateObject(objId, {
              markdown: content + linksSection,
              properties: [{ key: "__amemory_edges", text: JSON.stringify(forwardEdges) }]
            });

            // Backlinks + reverse edges on linked memories
            for (var bli = 0; bli < linkResults.length; bli++) {
              try {
                var blinkId = linkResults[bli].id;
                var linkedObj = client.getObject(blinkId);
                if (linkedObj && linkedObj.markdown) {
                  var backlink = "\n[" + meta.context.substring(0, 60) + "](anytype://object?objectId=" + objId + ")    \n";
                  var existingMd = linkedObj.markdown;
                  if (existingMd.indexOf("## Links") === -1) {
                    existingMd += "\n\n## Links" + backlink;
                  } else {
                    existingMd += backlink;
                  }

                  // Step 4d: Append reverse edge to linked memory's existing edges
                  var existingEdgesStr = linkedObj.__amemory_edges || "[]";
                  var existingEdges = [];
                  try { existingEdges = JSON.parse(existingEdgesStr); } catch (e) { existingEdges = []; }
                  if (!Array.isArray(existingEdges)) existingEdges = [];
                  existingEdges.push({
                    to: objId,
                    type: reverseEdgeType(linkResults[bli].type),
                    strength: candidateSims[blinkId] || 0.5
                  });

                  client.updateObject(blinkId, {
                    markdown: existingMd,
                    properties: [{ key: "__amemory_edges", text: JSON.stringify(existingEdges) }]
                  });
                }
              } catch (e) {}
            }

            // Extract plain IDs for Ps3 evolution (backward compat)
            var linkIds = linkResults.map(function(lr) { return lr.id; });

            // Ps3: Memory evolution — batched LLM calls for speed
            if (enableEvolution) {
              try {
                // Phase 1: Gather data and build all evolution prompts
                var evoItems = []; // { id, existingData, fullObj, prompt }
                for (var ei = 0; ei < linkIds.length; ei++) {
                  var existingData = null;
                  for (var xi = 0; xi < topCandidates.length; xi++) {
                    if (topCandidates[xi].id === linkIds[ei]) { existingData = topCandidates[xi]; break; }
                  }
                  if (!existingData) continue;
                  var evoFullObj = client.getObject(linkIds[ei]);
                  if (!evoFullObj) continue;
                  var evoPrompt = buildEvolvePrompt(
                    { content: content, context: meta.context, keywords: meta.keywords.join(", ") },
                    { content: evoFullObj.markdown || "", context: existingData.context, keywords: existingData.keywords, tags: (existingData.tags || []).join(", ") },
                    existingData.similarity
                  );
                  evoItems.push({ id: linkIds[ei], existingData: existingData, fullObj: evoFullObj, prompt: evoPrompt });
                }

                if (evoItems.length > 0) {
                  // Phase 2: Run all evolution LLM calls in parallel
                  var evoPrompts = evoItems.map(function(item) { return item.prompt; });
                  var evoResponses = llm.completeBatch(evoPrompts, "reason");

                  // Phase 3: Parse results, collect items that need re-embedding
                  var embTexts = [];   // texts to embed
                  var embIdxMap = [];  // index in evoItems for each embed text
                  var evoResults = []; // parsed evolution results

                  for (var ri = 0; ri < evoItems.length; ri++) {
                    var evoResult = parseEvolveResponse(evoResponses[ri]);
                    evoResults.push(evoResult);
                    if (evoResult && evoResult.should_evolve && (evoResult.new_context || evoResult.new_keywords)) {
                      var bodyText = (evoItems[ri].fullObj && evoItems[ri].fullObj.markdown) ? evoItems[ri].fullObj.markdown : "";
                      var newTags = evoResult.new_tags ? evoResult.new_tags.join(" ") : (evoItems[ri].existingData.tags || []).join(" ");
                      var newEmbText = bodyText + " " + (evoResult.new_context || evoItems[ri].existingData.context)
                        + " " + (evoResult.new_keywords || evoItems[ri].existingData.keywords) + " " + newTags;
                      embTexts.push(newEmbText);
                      embIdxMap.push(ri);
                    }
                  }

                  // Phase 4: Batch re-embed all evolved memories in one API call
                  var newEmbeddings = [];
                  if (embTexts.length > 0) {
                    try { newEmbeddings = llm.embedBatch(embTexts); } catch(e) {}
                  }

                  // Phase 5: Apply updates (Anytype API calls — fast, sequential is fine)
                  for (var ui = 0; ui < evoItems.length; ui++) {
                    var evoR = evoResults[ui];
                    if (!evoR || !evoR.should_evolve) continue;
                    try {
                      var updateProps = [];
                      if (evoR.new_context) updateProps.push({ key: "__amemory_context", text: evoR.new_context });
                      if (evoR.new_keywords) updateProps.push({ key: "__amemory_keywords", text: evoR.new_keywords });

                      // Find this item's embedding in the batch result
                      for (var embI = 0; embI < embIdxMap.length; embI++) {
                        if (embIdxMap[embI] === ui && newEmbeddings[embI]) {
                          updateProps.push({ key: "__amemory_vector", text: encodeVector(newEmbeddings[embI]) });
                          break;
                        }
                      }

                      if (updateProps.length > 0) {
                        client.updateObject(evoItems[ui].id, { properties: updateProps });
                      }
                      if (evoR.new_tags && evoR.new_tags.length > 0) {
                        for (var eti = 0; eti < evoR.new_tags.length; eti++) {
                          try { client.addTag(evoItems[ui].id, "amemory_" + evoR.new_tags[eti]); } catch(e) {}
                        }
                      }
                    } catch(e) {}
                  }
                }
              } catch(e) {}
            }
          }
        }
      } catch (e) {}
    }

    // Ps4: Lightweight verify on the newly created memory
    var verifyResult = verifyMemoryData(
      { name: meta.context.substring(0, 80), context: meta.context, keywords: meta.keywords.join(", "), tags: meta.tags, vec: embeddingHex, body: content },
      0  // skip link check on new memories
    );

    var addResult = {
      ok: true, id: objId, context: meta.context, keywords: meta.keywords, tags: meta.tags,
      category: meta.category, entities: meta.entities,
      confidence: meta.confidence, importance: meta.importance,
      hasVector: !!embedding, embedWarning: embedWarning,
      verify: verifyResult
    };

    if (debugHook) debugHook("add_memory", {
      content: content.substring(0, 200),
      category: meta.category,
      keywords: meta.keywords,
      entities: meta.entities,
      confidence: meta.confidence,
      importance: meta.importance,
      hasVector: !!embedding,
      linksCreated: linkResults ? linkResults.length : 0
    });

    return addResult;
  }

  // ── extractLinkedIds: parse anytype:// links from markdown ───────────

  function extractLinkedIds(objectId) {
    try {
      var obj = client.getObject(objectId);
      if (!obj || !obj.markdown) return [];
      var ids = [];
      var regex = /anytype:\/\/object\?objectId=([a-z0-9._-]+)/g;
      var match;
      while ((match = regex.exec(obj.markdown)) !== null) {
        if (match[1] !== objectId) ids.push(match[1]);
      }
      return ids;
    } catch (e) {
      return [];
    }
  }

  // ── rewriteQuery — LLM-driven query expansion for memory recall ─────
  // FOLLOWUP: query rewriting subprompt for implicit temporal/intent resolution
  // Converts a single user query (+ optional chat history) into multiple
  // optimized search queries. Each sub-query targets a different recall angle.

  function rewriteQuery(query, chatHistory) {
    var historySnippet = "";
    if (chatHistory) {
      // Take last ~500 chars of chat history for context
      historySnippet = chatHistory.length > 500 ? chatHistory.substring(chatHistory.length - 500) : chatHistory;
    }

    var prompt = "You convert a user query into optimized memory search queries.\n"
      + "Given the user's message" + (historySnippet ? " and recent chat context" : "") + ", return 1-3 search queries that would best retrieve relevant memories.\n\n"
      + "Each query should target a different angle:\n"
      + "- Rephrase ambiguous references (\"that\", \"it\", \"the thing\") using chat context\n"
      + "- Expand abbreviations (DB→database, k8s→kubernetes, etc)\n"
      + "- Extract the core intent — what information is the user actually looking for?\n"
      + "- If temporal context is implied but not stated, add it as a separate query\n"
      + "- If the query is already clear and specific, return just 1 query (the original or slightly improved)\n\n"
      + "Return JSON: {\"queries\": [{\"text\": \"search terms\", \"weight\": 1.0}]}\n"
      + "- weight: 1.0 for primary query, 0.7 for supplementary angles\n"
      + "- Maximum 3 queries. Prefer fewer, more targeted queries.\n"
      + "- Return ONLY the JSON, no markdown or extra text.\n\n"
      + "User message: " + query + "\n";

    if (historySnippet) {
      prompt += "\nRecent chat context:\n" + historySnippet + "\n";
    }

    var startTime = Date.now();
    var resp = llm.classify(prompt);
    var elapsed = Date.now() - startTime;

    var parsed = llm.parseJSON(resp);
    if (!parsed || !parsed.queries || !Array.isArray(parsed.queries) || parsed.queries.length === 0) {
      // Fallback: return original query
      return { queries: [{ text: query, weight: 1.0 }], elapsed: elapsed, fallback: true };
    }

    // Validate and normalize
    var queries = [];
    for (var i = 0; i < parsed.queries.length && i < 3; i++) {
      var q = parsed.queries[i];
      if (q && typeof q.text === "string" && q.text.length > 0) {
        queries.push({
          text: q.text,
          weight: (typeof q.weight === "number" && q.weight > 0 && q.weight <= 1.0) ? q.weight : 1.0
        });
      }
    }

    if (queries.length === 0) {
      return { queries: [{ text: query, weight: 1.0 }], elapsed: elapsed, fallback: true };
    }

    return { queries: queries, elapsed: elapsed, fallback: false };
  }

  // ── searchWithRewrite — multi-query search with merge/rerank ────────
  // Rewrites the query and embeds the raw query IN PARALLEL via fetchBatch,
  // then uses pre-computed vectors to avoid redundant embedding calls.

  function searchWithRewrite(query, k, chatHistory, searchOpts) {
    k = k || topK;
    searchOpts = searchOpts || {};

    // Build rewrite prompt (same as rewriteQuery but we need the prompt for fetchBatch)
    var historySnippet = "";
    if (chatHistory) {
      historySnippet = chatHistory.length > 500 ? chatHistory.substring(chatHistory.length - 500) : chatHistory;
    }
    var rewritePrompt = "You convert a user query into optimized memory search queries.\n"
      + "Given the user's message" + (historySnippet ? " and recent chat context" : "") + ", return 1-3 search queries that would best retrieve relevant memories.\n\n"
      + "Each query should target a different angle:\n"
      + "- Rephrase ambiguous references (\"that\", \"it\", \"the thing\") using chat context\n"
      + "- Expand abbreviations (DB→database, k8s→kubernetes, etc)\n"
      + "- Extract the core intent — what information is the user actually looking for?\n"
      + "- If temporal context is implied but not stated, add it as a separate query\n"
      + "- If the query is already clear and specific, return just 1 query (the original or slightly improved)\n\n"
      + "Return JSON: {\"queries\": [{\"text\": \"search terms\", \"weight\": 1.0}]}\n"
      + "- weight: 1.0 for primary query, 0.7 for supplementary angles\n"
      + "- Maximum 3 queries. Prefer fewer, more targeted queries.\n"
      + "- Return ONLY the JSON, no markdown or extra text.\n\n"
      + "User message: " + query + "\n";
    if (historySnippet) {
      rewritePrompt += "\nRecent chat context:\n" + historySnippet + "\n";
    }

    // Run rewrite LLM + raw query embedding in PARALLEL
    var startTime = Date.now();
    var rewriteFetchArgs = llm.buildCompleteFetchArgs(rewritePrompt, "classify");
    var embedFetchArgs = llm.buildEmbedFetchArgs([query]);
    var parallel = fetchBatch([rewriteFetchArgs, embedFetchArgs]);
    var parallelElapsed = Date.now() - startTime;

    // Parse rewrite result
    var rewriteProvider = rewriteFetchArgs[rewriteFetchArgs.length - 1];
    var rewriteResp = llm.parseCompleteResult(parallel[0], rewriteProvider);
    var parsed = llm.parseJSON(rewriteResp);
    var queries = [];
    if (parsed && parsed.queries && Array.isArray(parsed.queries)) {
      for (var i = 0; i < parsed.queries.length && i < 3; i++) {
        var q = parsed.queries[i];
        if (q && typeof q.text === "string" && q.text.length > 0) {
          queries.push({
            text: q.text,
            weight: (typeof q.weight === "number" && q.weight > 0 && q.weight <= 1.0) ? q.weight : 1.0
          });
        }
      }
    }
    if (queries.length === 0) {
      queries = [{ text: query, weight: 1.0 }];
    }
    var rewrite = { queries: queries, elapsed: parallelElapsed, fallback: queries.length === 1 && queries[0].text === query };

    // Parse raw query embedding (computed in parallel with rewrite)
    var rawVec = "";
    try {
      var rawEmbeds = llm.parseEmbedResult(parallel[1]);
      if (rawEmbeds && rawEmbeds[0]) {
        rawVec = encodeVector(rawEmbeds[0]);
      }
    } catch(e) {}

    // Single query shortcut — use pre-computed embedding
    if (rewrite.queries.length === 1) {
      var sqText = rewrite.queries[0].text;
      // If rewritten query is same/similar to original, reuse pre-computed vector
      var sqOpts = {};
      for (var sk in searchOpts) sqOpts[sk] = searchOpts[sk];
      if (sqText === query || sqText.toLowerCase() === query.toLowerCase()) {
        sqOpts.queryVec = rawVec;
      }
      var results = searchMemories(sqText, k, sqOpts);
      return { results: results, rewrite: rewrite };
    }

    // Multi-query: embed all rewritten queries in one batch, then search with pre-computed vectors
    var textsToEmbed = [];
    var queryVecs = [];
    for (var qi = 0; qi < rewrite.queries.length; qi++) {
      var qt = rewrite.queries[qi].text;
      if (qt === query || qt.toLowerCase() === query.toLowerCase()) {
        queryVecs.push(rawVec); // reuse pre-computed
      } else {
        textsToEmbed.push({ idx: qi, text: qt });
        queryVecs.push(null); // placeholder
      }
    }

    // Batch embed the new query texts
    if (textsToEmbed.length > 0) {
      try {
        var batchTexts = textsToEmbed.map(function(t) { return t.text; });
        var batchEmbeds = llm.embedBatch(batchTexts);
        for (var bi = 0; bi < textsToEmbed.length; bi++) {
          if (batchEmbeds[bi]) {
            queryVecs[textsToEmbed[bi].idx] = encodeVector(batchEmbeds[bi]);
          }
        }
      } catch(e) {}
    }

    // Run searchMemories per sub-query with pre-computed vectors, merge by best score
    var byId = {};
    for (var qi2 = 0; qi2 < rewrite.queries.length; qi2++) {
      var subQuery = rewrite.queries[qi2];
      var subOpts = {};
      for (var sk2 in searchOpts) subOpts[sk2] = searchOpts[sk2];
      if (queryVecs[qi2]) subOpts.queryVec = queryVecs[qi2];
      var subResults = searchMemories(subQuery.text, k * 2, subOpts);
      for (var ri = 0; ri < subResults.length; ri++) {
        var r = subResults[ri];
        var weightedScore = (r.similarity || 0) * subQuery.weight;
        if (!byId[r.id] || weightedScore > byId[r.id].bestScore) {
          byId[r.id] = { result: r, bestScore: weightedScore };
        }
      }
    }

    var merged = Object.values(byId);
    merged.sort(function(a, b) { return b.bestScore - a.bestScore; });
    var finalResults = [];
    for (var mi = 0; mi < Math.min(merged.length, k); mi++) {
      var item = merged[mi];
      item.result.similarity = item.bestScore;
      finalResults.push(item.result);
    }

    if (debugHook) debugHook("search_rewrite", {
      originalQuery: query,
      queryCount: rewrite.queries.length,
      queries: rewrite.queries.map(function(q) { return { text: q.text, weight: q.weight }; }),
      elapsed: rewrite.elapsed,
      fallback: rewrite.fallback,
      resultCount: finalResults.length
    });

    return { results: finalResults, rewrite: rewrite };
  }

  // ── searchMemories ────────────────────────────────────────────────────

  function searchMemories(query, k, searchOpts) {
    k = k || topK;
    searchOpts = searchOpts || {};
    var followLinks = searchOpts.followLinks !== undefined ? searchOpts.followLinks : enableLinks;
    var useFts = searchOpts.fts !== undefined ? searchOpts.fts : (ftsWeight > 0);

    // When the caller narrows with opts.categories, the category filter IS
    // the primary relevance signal — cutting by cosine would defeat the point
    // (e.g. a general 'edit before create' preference won't score close to
    // 'book reading notes' but is still THE match the caller wants). Drop
    // minSimilarity to 0 on category-filtered calls unless explicitly set.
    // Ranking stays cosine-based; only the cutoff relaxes.
    var minSim = (searchOpts.minSimilarity !== undefined)
      ? searchOpts.minSimilarity
      : ((searchOpts.categories && searchOpts.categories.length > 0) ? 0 : minSimilarity);

    // Accept pre-computed query vector to avoid redundant embedding calls
    var queryVec = searchOpts.queryVec || "";
    if (!queryVec) {
      var queryEmb = getEmbedding(query);
      queryVec = queryEmb ? encodeVector(queryEmb) : "";
    }
    var allMems = loadAllMemories(client, typeKey);
    if (allMems.length === 0) return [];

    // Category filter (absolute — applies to both initial retrieval and
    // link expansion via the shared memById map).
    var catFilter = _buildCategoryFilter(searchOpts.categories, searchOpts.excludeCategories);
    if (catFilter) {
      allMems = allMems.filter(catFilter);
      if (allMems.length === 0) return [];
    }

    // Chat-id filter — only narrows chat_chunk memories; other categories
    // stay space-global. `chatIdFilter` is one of "all" (default) / "only" /
    // "exclude". When unset or "all", this is a no-op.
    var chatFilter = _buildChatIdFilter(searchOpts.chatId, searchOpts.chatIdFilter);
    if (chatFilter) {
      allMems = allMems.filter(chatFilter);
      if (allMems.length === 0) return [];
    }

    // Build lookup by id for link expansion
    var memById = {};
    for (var mi = 0; mi < allMems.length; mi++) {
      memById[allMems[mi].id] = allMems[mi];
    }

    if (!queryVec) {
      return allMems.slice(0, k).map(function(m) {
        return { id: m.id, name: m.name, context: m.context, keywords: m.keywords, tags: m.tags, createdDate: m.createdDate,
          category: m.category, confidence: m.confidence, importance: m.importance,
          salience: m.salience, accessCount: m.accessCount, validFrom: m.validFrom,
          entities: m.entities, similarity: 0 };
      });
    }

    // FTS scores from Anytype search (Tantivy BM25)
    var ftsScores = {};
    if (useFts) {
      ftsScores = ftsSearch(client, query, typeKey, k * 3);
    }

    // Step 3: Detect temporal reference for boost, compute current time for decay
    var temporalWindow = detectTemporalReference(query);
    var nowMs = new Date().getTime();

    // Step 4b: Extract query keywords for entity matching
    var queryKeywords = query.toLowerCase().replace(/[^a-z0-9\s]/g, " ").split(/\s+/).filter(function(w) { return w.length >= 3; });

    // Step 5: Multi-dimensional scoring weights (configurable via opts).
    // chunkBoost (slice 1b) gives chat_chunk memories a small additive edge —
    // chunks carry richer narrative context than typed memories, worth ~0.05
    // of the combined score when they're in the candidate pool.
    var W = opts.recallWeights || { cosine: 0.45, fts: 0.10, entity: 0.15, temporal: 0.10, salience: 0.08, confidence: 0.07, chunkBoost: 0.05 };

    var scored = [];
    for (var i = 0; i < allMems.length; i++) {
      var cosine = cosineSimilarity(queryVec, allMems[i].vec);
      var fts = ftsScores[allMems[i].id] || 0;
      var chunkScore = (allMems[i].tags && allMems[i].tags.indexOf("chat_chunk") !== -1) ? 1 : 0;

      // Step 3: Temporal decay modifier
      var temporal = computeTemporalModifier(allMems[i], temporalWindow, nowMs);

      // Step 4b: Entity-based scoring
      var entityScore = 0;
      if (queryKeywords.length > 0 && allMems[i].entities) {
        var memEntities = allMems[i].entities.split(",").map(function(e) { return e.trim().toLowerCase(); }).filter(function(e) { return e.length > 0; });
        if (memEntities.length > 0) {
          var entityMatches = 0;
          for (var qi = 0; qi < queryKeywords.length; qi++) {
            var qw = queryKeywords[qi];
            var matched = false;
            for (var ei = 0; ei < memEntities.length; ei++) {
              if (memEntities[ei] === qw) {
                // Exact match: full score
                entityMatches += 1.0;
                matched = true;
                break;
              }
            }
            if (!matched) {
              // Fuzzy match: editDistance <= 2 AND distance/length < 0.3
              for (var ei = 0; ei < memEntities.length; ei++) {
                var dist = editDistance(qw, memEntities[ei]);
                var maxLen = qw.length > memEntities[ei].length ? qw.length : memEntities[ei].length;
                if (dist <= 2 && maxLen > 0 && (dist / maxLen) < 0.3) {
                  entityMatches += 0.7;
                  break;
                }
              }
            }
          }
          entityScore = entityMatches / queryKeywords.length;
          if (entityScore > 1.0) entityScore = 1.0;
        }
      }

      // Step 5: Multi-dimensional score (slice 1b adds chunkBoost term)
      var salience = allMems[i].salience;
      var confidence = allMems[i].confidence;
      var combined = cosine * W.cosine + fts * W.fts + entityScore * W.entity
        + temporal * W.temporal + (salience / 10) * W.salience + (confidence / 10) * W.confidence
        + chunkScore * (W.chunkBoost || 0);

      scored.push({
        id: allMems[i].id,
        name: allMems[i].name,
        context: allMems[i].context,
        keywords: allMems[i].keywords,
        tags: allMems[i].tags,
        createdDate: allMems[i].createdDate,
        category: allMems[i].category,
        confidence: allMems[i].confidence,
        importance: allMems[i].importance,
        salience: allMems[i].salience,
        accessCount: allMems[i].accessCount,
        validFrom: allMems[i].validFrom,
        entities: allMems[i].entities,
        edges: allMems[i].edges,
        chatId: allMems[i].chatId || "",
        similarity: combined,
        _cosine: cosine,
        _fts: fts,
        _temporal: temporal,
        _entity: entityScore
      });
    }

    // Sort by combined score descending, filter by threshold (on cosine
    // component). minSim relaxes to 0 on category-filtered calls — see
    // the minSim derivation above.
    scored.sort(function(a, b) { return b.similarity - a.similarity; });
    var filtered = [];
    for (var fi = 0; fi < scored.length; fi++) {
      if (scored[fi]._cosine >= minSim) {
        filtered.push(scored[fi]);
      }
    }

    // Take initial top-k
    var topResults = filtered.slice(0, k);

    // Graph expansion: follow markdown links AND typed edges 1 hop from top results
    if (followLinks && topResults.length > 0) {
      var seen = {};
      for (var ti = 0; ti < topResults.length; ti++) {
        seen[topResults[ti].id] = true;
      }

      // Helper: score an expanded (linked/edge) memory using multi-dimensional formula
      function scoreExpanded(linked, edgeStrength, edgePenalty) {
        var eCosine = cosineSimilarity(queryVec, linked.vec);
        if (eCosine < minSim) return null;
        var eFts = ftsScores[linked.id] || 0;
        var eTemporal = computeTemporalModifier(linked, temporalWindow, nowMs);

        // Entity scoring for expanded node
        var eEntityScore = 0;
        if (queryKeywords.length > 0 && linked.entities) {
          var eMemEntities = linked.entities.split(",").map(function(e) { return e.trim().toLowerCase(); }).filter(function(e) { return e.length > 0; });
          if (eMemEntities.length > 0) {
            var eEntityMatches = 0;
            for (var eqi = 0; eqi < queryKeywords.length; eqi++) {
              var eqw = queryKeywords[eqi];
              var eMatched = false;
              for (var eei = 0; eei < eMemEntities.length; eei++) {
                if (eMemEntities[eei] === eqw) { eEntityMatches += 1.0; eMatched = true; break; }
              }
              if (!eMatched) {
                for (var eei = 0; eei < eMemEntities.length; eei++) {
                  var eDist = editDistance(eqw, eMemEntities[eei]);
                  var eMaxLen = eqw.length > eMemEntities[eei].length ? eqw.length : eMemEntities[eei].length;
                  if (eDist <= 2 && eMaxLen > 0 && (eDist / eMaxLen) < 0.3) { eEntityMatches += 0.7; break; }
                }
              }
            }
            eEntityScore = eEntityMatches / queryKeywords.length;
            if (eEntityScore > 1.0) eEntityScore = 1.0;
          }
        }

        var eSalience = linked.salience;
        var eConfidence = linked.confidence;
        var eChunkScore = (linked.tags && linked.tags.indexOf("chat_chunk") !== -1) ? 1 : 0;
        var eCombined = eCosine * W.cosine + eFts * W.fts + eEntityScore * W.entity
          + eTemporal * W.temporal + (eSalience / 10) * W.salience + (eConfidence / 10) * W.confidence
          + eChunkScore * (W.chunkBoost || 0);

        // Apply edge strength and penalty (for contradicts edges)
        if (edgeStrength !== undefined) eCombined = eCombined * edgeStrength * (edgePenalty || 1.0);

        return {
          id: linked.id, name: linked.name, context: linked.context,
          keywords: linked.keywords, tags: linked.tags, createdDate: linked.createdDate,
          category: linked.category, confidence: linked.confidence,
          importance: linked.importance, salience: linked.salience,
          accessCount: linked.accessCount, validFrom: linked.validFrom,
          entities: linked.entities, chatId: linked.chatId || "",
          similarity: eCombined,
          _cosine: eCosine, _fts: eFts, _temporal: eTemporal, _entity: eEntityScore,
          viaLink: true
        };
      }

      var expanded = [];
      for (var ti = 0; ti < topResults.length; ti++) {
        // Follow markdown links (existing behavior)
        var linkedIds = extractLinkedIds(topResults[ti].id);
        for (var li = 0; li < linkedIds.length; li++) {
          var lid = linkedIds[li];
          if (seen[lid]) continue;
          seen[lid] = true;

          var linked = memById[lid];
          if (!linked) continue;

          var expResult = scoreExpanded(linked);
          if (expResult) expanded.push(expResult);
        }

        // Step 4c: Follow typed edges from __amemory_edges
        var edgesJson = topResults[ti].edges || (memById[topResults[ti].id] && memById[topResults[ti].id].edges) || "[]";
        var edges = [];
        try { edges = JSON.parse(edgesJson); } catch (e) { edges = []; }
        if (!Array.isArray(edges)) edges = [];

        var followableEdgeTypes = ["caused_by", "leads_to", "related_to", "supported_by", "contradicts"];
        for (var edi = 0; edi < edges.length; edi++) {
          var edge = edges[edi];
          if (!edge || !edge.to) continue;
          if (followableEdgeTypes.indexOf(edge.type) === -1) continue;
          if (seen[edge.to]) continue;
          seen[edge.to] = true;

          var edgeLinked = memById[edge.to];
          if (!edgeLinked) continue;

          var edgeStrength = (typeof edge.strength === "number") ? edge.strength : 1.0;
          var edgePenalty = (edge.type === "contradicts") ? 0.5 : 1.0;

          var edgeResult = scoreExpanded(edgeLinked, edgeStrength, edgePenalty);
          if (edgeResult) {
            edgeResult.viaEdge = edge.type;
            expanded.push(edgeResult);
          }
        }
      }

      // Merge expanded into results, re-sort, re-slice to k
      if (expanded.length > 0) {
        var merged = topResults.concat(expanded);
        merged.sort(function(a, b) { return b.similarity - a.similarity; });
        topResults = merged.slice(0, k);
      }
    }

    // Step 5: Increment __amemory_access_count on recalled memories (best-effort)
    for (var ai = 0; ai < topResults.length; ai++) {
      try {
        var currentCount = topResults[ai].accessCount || 0;
        client.updateObject(topResults[ai].id, {
          properties: [{ key: "__amemory_access_count", number: currentCount + 1 }]
        });
      } catch (e) {
        // best-effort — don't fail search on update error
      }
    }

    if (debugHook) debugHook("search_score", {
      query: query,
      totalScanned: allMems ? allMems.length : 0,
      resultCount: topResults.length,
      topResults: topResults.slice(0, 5).map(function(r) {
        return { id: r.id, name: (r.name || "").substring(0, 60), similarity: r.similarity,
                 _cosine: r._cosine, _fts: r._fts, _temporal: r._temporal, _entity: r._entity,
                 viaLink: r.viaLink || false, viaEdge: r.viaEdge || "" };
      })
    });

    return topResults;
  }

  // ── getContext ─────────────────────────────────────────────────────────

  function getContext(query, k, contextOpts) {
    k = k || topK;
    contextOpts = contextOpts || {};
    var results;
    if (contextOpts.chatHistory) {
      var rw = searchWithRewrite(query, k, contextOpts.chatHistory);
      results = rw.results;
    } else {
      results = searchMemories(query, k);
    }
    if (results.length === 0) return null;

    var lines = ["## Relevant memories (A-mem)\n"];
    for (var i = 0; i < results.length; i++) {
      var r = results[i];
      var fullObj = client.getObject(r.id);
      var body = (fullObj && fullObj.markdown) ? fullObj.markdown : r.context;
      if (body.length > 500) body = body.substring(0, 497) + "...";

      // Show creation date prominently (when this knowledge was recorded)
      var dateLine = "";
      if (r.createdDate) {
        dateLine = "**Created:** " + r.createdDate.substring(0, 10);
      }

      lines.push("### Memory " + (i + 1) + " (similarity: " + r.similarity.toFixed(3) + ")");
      if (dateLine) lines.push(dateLine);
      lines.push("**Context:** " + r.context);
      lines.push("**Keywords:** " + r.keywords);
      lines.push(body);
      lines.push("");
    }

    var contextResult = lines.join("\n");
    if (debugHook) debugHook("get_context", {
      query: query.substring(0, 100),
      resultCount: results.length
    });
    return contextResult;
  }

  // ── getClusteredContext ──────────────────────────────────────────────

  function getClusteredContext(query, k) {
    k = k || topK;
    var pool = searchMemories(query, k * 3);
    if (pool.length === 0) return null;

    // Group by primary tag (first tag). Untagged go in "_untagged".
    var clusters = {};
    var clusterOrder = [];
    for (var i = 0; i < pool.length; i++) {
      var tag = (pool[i].tags && pool[i].tags.length > 0) ? pool[i].tags[0] : "_untagged";
      if (!clusters[tag]) {
        clusters[tag] = [];
        clusterOrder.push(tag);
      }
      clusters[tag].push(pool[i]);
    }

    var lines = ["## Relevant memories (A-mem, clustered)\n"];
    var clusterCount = 0;

    for (var ci = 0; ci < clusterOrder.length; ci++) {
      if (clusterCount >= k) break;
      var tag = clusterOrder[ci];
      var members = clusters[tag];

      if (members.length === 1) {
        var r = members[0];
        var fullObj = client.getObject(r.id);
        var body = (fullObj && fullObj.markdown) ? fullObj.markdown : r.context;
        if (body.length > 500) body = body.substring(0, 497) + "...";

        lines.push("### " + tag + " (1 memory, similarity: " + r.similarity.toFixed(3) + ")");
        lines.push("**Context:** " + r.context);
        lines.push("**Keywords:** " + r.keywords);
        lines.push(body);
        lines.push("");
      } else {
        var bestSim = members[0].similarity;
        var memberTexts = "";
        for (var mi = 0; mi < members.length; mi++) {
          var fullObj = client.getObject(members[mi].id);
          var body = (fullObj && fullObj.markdown) ? fullObj.markdown : members[mi].context;
          if (body.length > 300) body = body.substring(0, 297) + "...";
          memberTexts += "- " + members[mi].context + ": " + body + "\n";
        }

        var summary = summarizeCluster(tag, memberTexts, query);

        lines.push("### " + tag + " (" + members.length + " memories, best similarity: " + bestSim.toFixed(3) + ")");
        lines.push(summary);
        lines.push("");
      }
      clusterCount++;
    }

    return lines.join("\n");
  }

  // ── getCategories ──────────────────────────────────────────────────────

  function getCategories() {
    var allMems = loadAllMemories(client, typeKey);
    var seen = {};
    var categories = [];
    for (var i = 0; i < allMems.length; i++) {
      var tags = allMems[i].tags;
      for (var j = 0; j < tags.length; j++) {
        if (!seen[tags[j]]) {
          seen[tags[j]] = 0;
          categories.push(tags[j]);
        }
        seen[tags[j]]++;
      }
    }
    categories.sort(function(a, b) { return seen[b] - seen[a]; });
    var result = [];
    for (var i = 0; i < categories.length; i++) {
      result.push({ name: categories[i], count: seen[categories[i]] });
    }
    return result;
  }

  // listCategories — returns { builtin, observed }.
  // `builtin` is the fixed MEMORY_CATEGORIES preference list. `observed` is
  // the set of categories actually present on memories in this space (derived
  // from each memory's detected category, not from tag frequency), with counts
  // and a `builtin: true|false` flag so the agent can tell recommended vs
  // invented at a glance.
  function listCategories() {
    var allMems = loadAllMemories(client, typeKey);
    var counts = {};
    for (var i = 0; i < allMems.length; i++) {
      var c = allMems[i].category;
      if (!c) continue;
      counts[c] = (counts[c] || 0) + 1;
    }
    var observed = [];
    for (var name in counts) {
      if (!counts.hasOwnProperty(name)) continue;
      observed.push({
        name: name,
        count: counts[name],
        builtin: MEMORY_CATEGORIES.indexOf(name) !== -1
      });
    }
    observed.sort(function(a, b) { return b.count - a.count; });
    return { builtin: MEMORY_CATEGORIES.slice(), observed: observed };
  }

  function getStats() {
    var allMems = loadAllMemories(client, typeKey);
    var top3 = allMems.slice(0, 3).map(function(m) {
      return { name: m.name, tags: m.tags };
    });
    return { count: allMems.length, top: top3 };
  }

  // ── Ps4: verifyMemory — full quality check on a single memory by ID ───

  function verifyMemory(memoryId) {
    var fullObj = client.getObject(memoryId);
    if (!fullObj) return { status: "FAIL", score: 0, issues: [{ severity: "FAIL", check: "exists", msg: "Memory not found" }] };

    var vectorHex = fullObj.__amemory_vector || "";

    var allMems = loadAllMemories(client, typeKey);

    var memData = {
      name: fullObj.name || "",
      context: fullObj.__amemory_context || "",
      keywords: fullObj.__amemory_keywords || "",
      tags: stripTagPrefix(getTagKeys(fullObj, "tag") || []),
      vec: vectorHex,
      body: fullObj.markdown || ""
    };

    return verifyMemoryData(memData, allMems.length);
  }

  // ── Ps4: verifyAll — batch quality check ──────────────────────────────

  function verifyAll() {
    var allMems = loadAllMemoriesFull(client, typeKey);
    var total = allMems.length;
    var pass = 0;
    var warn = 0;
    var fail = 0;
    var worstMemories = [];

    for (var i = 0; i < allMems.length; i++) {
      var m = allMems[i];
      var result = verifyMemoryData(m, total);
      if (result.status === "PASS") pass++;
      else if (result.status === "WARN") warn++;
      else fail++;

      if (result.score < 4) {
        worstMemories.push({ id: m.id, name: m.name, score: result.score, issues: result.issues });
      }
    }

    // Sort worst first
    worstMemories.sort(function(a, b) { return a.score - b.score; });

    return {
      total: total,
      pass: pass,
      warn: warn,
      fail: fail,
      healthScore: total > 0 ? Math.round((pass / total) * 100) : 0,
      worst: worstMemories.slice(0, 10)
    };
  }

  // ── Ps5: rethink — memory graph health audit ──────────────────────────

  function rethink(mode) {
    mode = mode || "full";
    var allMems = loadAllMemoriesFull(client, typeKey);
    var report = { mode: mode, totalMemories: allMems.length };

    if (mode === "full" || mode === "stale") {
      report.stale = findStaleMemories(allMems);
    }

    if (mode === "full" || mode === "duplicates") {
      report.duplicates = findDuplicateCandidates(allMems, duplicateThreshold);
    }

    if (mode === "full" || mode === "weak") {
      report.weak = findWeakMemories(allMems);
    }

    if (mode === "full" || mode === "audit") {
      // Run verify on all memories
      var pass = 0;
      var warn = 0;
      var fail = 0;
      for (var i = 0; i < allMems.length; i++) {
        var vr = verifyMemoryData(allMems[i], allMems.length);
        if (vr.status === "PASS") pass++;
        else if (vr.status === "WARN") warn++;
        else fail++;
      }
      report.audit = { pass: pass, warn: warn, fail: fail, healthPct: allMems.length > 0 ? Math.round((pass / allMems.length) * 100) : 0 };
    }

    // Summary line
    var summary = "Memory graph: " + allMems.length + " memories";
    if (report.stale) summary += ", " + report.stale.length + " stale";
    if (report.duplicates) summary += ", " + report.duplicates.length + " potential duplicates";
    if (report.weak) summary += ", " + report.weak.length + " weak";
    if (report.audit) summary += ", " + report.audit.healthPct + "% healthy";
    report.summary = summary;

    return report;
  }

  // ── Step 6: Context-Selective Preference/Lesson Injection ──────────────

  function getPreferencesAndLessons(query, maxItems) {
    maxItems = maxItems || 5;
    var SIMILARITY_THRESHOLD = 0.25;
    var MAX_CHARS_PER_ITEM = 500;

    var allMems = loadAllMemories(client, typeKey);
    if (allMems.length === 0) return null;

    // Filter to only preference and lesson categories
    var filtered = [];
    for (var i = 0; i < allMems.length; i++) {
      var cat = allMems[i].category;
      if (cat === "preference" || cat === "lesson") {
        filtered.push(allMems[i]);
      }
    }
    if (filtered.length === 0) return null;

    // Get query embedding
    var queryEmb = getEmbedding(query);
    if (!queryEmb) return null;
    var queryHex = encodeVector(queryEmb);

    // Score each filtered memory by cosine similarity against query
    var scored = [];
    for (var i = 0; i < filtered.length; i++) {
      var sim = cosineSimilarity(queryHex, filtered[i].vec);
      if (sim >= SIMILARITY_THRESHOLD) {
        scored.push({ mem: filtered[i], similarity: sim });
      }
    }
    if (scored.length === 0) return null;

    // Sort descending by similarity
    scored.sort(function(a, b) { return b.similarity - a.similarity; });

    // Take top-N
    if (scored.length > maxItems) scored = scored.slice(0, maxItems);

    // Separate into preferences and lessons
    var preferences = [];
    var lessons = [];
    for (var i = 0; i < scored.length; i++) {
      var ctx = scored[i].mem.context || scored[i].mem.name || "";
      if (ctx.length > MAX_CHARS_PER_ITEM) {
        ctx = ctx.substring(0, MAX_CHARS_PER_ITEM - 3) + "...";
      }
      if (scored[i].mem.category === "preference") {
        preferences.push(ctx);
      } else {
        lessons.push(ctx);
      }
    }

    // Format as markdown
    var lines = [];
    if (preferences.length > 0) {
      lines.push("## User Preferences");
      for (var i = 0; i < preferences.length; i++) {
        lines.push("- " + preferences[i]);
      }
    }
    if (lessons.length > 0) {
      if (lines.length > 0) lines.push("");
      lines.push("## Critical Lessons");
      for (var i = 0; i < lessons.length; i++) {
        lines.push("- " + lessons[i]);
      }
    }

    if (lines.length === 0) return null;
    return lines.join("\n");
  }

  // ── getSessionContext: recent episodes + active taskstates ────────────
  // Replaces compressed chat history — retrieves session-level context from A-Mem.
  // No embedding needed: just filter by category and sort by recency.

  function getSessionContext(maxItems) {
    maxItems = maxItems || 5;
    var MAX_CHARS_PER_ITEM = 300;

    var allMems = loadAllMemories(client, typeKey);
    if (allMems.length === 0) return null;

    // Filter to episode and taskstate categories
    var episodes = [];
    var taskstates = [];
    for (var i = 0; i < allMems.length; i++) {
      var cat = allMems[i].category;
      if (cat === "episode") {
        episodes.push(allMems[i]);
      } else if (cat === "taskstate") {
        taskstates.push(allMems[i]);
      }
    }

    if (episodes.length === 0 && taskstates.length === 0) return null;

    // Sort by creation date descending (most recent first)
    function byDateDesc(a, b) {
      var da = a.createdDate || "";
      var db = b.createdDate || "";
      return da > db ? -1 : da < db ? 1 : 0;
    }
    episodes.sort(byDateDesc);
    taskstates.sort(byDateDesc);

    // Take most recent items, preferring taskstates (they're more actionable)
    var taskstateSlots = Math.min(taskstates.length, Math.ceil(maxItems / 2));
    var episodeSlots = Math.min(episodes.length, maxItems - taskstateSlots);

    var lines = [];

    if (taskstateSlots > 0) {
      lines.push("## Work in progress");
      for (var i = 0; i < taskstateSlots; i++) {
        var ctx = taskstates[i].context || taskstates[i].name || "";
        if (ctx.length > MAX_CHARS_PER_ITEM) ctx = ctx.substring(0, MAX_CHARS_PER_ITEM - 3) + "...";
        var date = taskstates[i].createdDate ? " (" + taskstates[i].createdDate.substring(0, 10) + ")" : "";
        lines.push("- " + ctx + date);
      }
    }

    if (episodeSlots > 0) {
      if (lines.length > 0) lines.push("");
      lines.push("## Recent activity");
      for (var i = 0; i < episodeSlots; i++) {
        var ctx = episodes[i].context || episodes[i].name || "";
        if (ctx.length > MAX_CHARS_PER_ITEM) ctx = ctx.substring(0, MAX_CHARS_PER_ITEM - 3) + "...";
        var date = episodes[i].createdDate ? " (" + episodes[i].createdDate.substring(0, 10) + ")" : "";
        lines.push("- " + ctx + date);
      }
    }

    if (lines.length === 0) return null;
    return lines.join("\n");
  }

  // ── getCodegenLessons: retrieve lessons from past tracer fixes ────────
  // Filters to amemory_codegen_lesson tagged memories, scores by similarity
  // to the goal context. Returns formatted markdown for codegen prompt injection.

  function getCodegenLessons(goalTitle, k) {
    k = k || 3;
    var MAX_CHARS_PER_ITEM = 200;

    var allMems = loadAllMemories(client, typeKey);
    if (allMems.length === 0) return null;

    // Filter to codegen_lesson tagged memories
    var lessons = [];
    for (var i = 0; i < allMems.length; i++) {
      var tags = allMems[i].tags || [];
      for (var ti = 0; ti < tags.length; ti++) {
        if (tags[ti] === "codegen_lesson") {
          lessons.push(allMems[i]);
          break;
        }
      }
    }
    if (lessons.length === 0) return null;

    // Score by cosine similarity to goal title
    var queryEmb = getEmbedding(goalTitle);
    if (!queryEmb) {
      // No embedding — return most recent lessons as fallback
      lessons.sort(function(a, b) {
        var da = a.createdDate || "";
        var db = b.createdDate || "";
        return da > db ? -1 : da < db ? 1 : 0;
      });
      var fallback = lessons.slice(0, k);
      var lines = ["## Known pitfalls"];
      for (var i = 0; i < fallback.length; i++) {
        var ctx = fallback[i].context || fallback[i].name || "";
        if (ctx.length > MAX_CHARS_PER_ITEM) ctx = ctx.substring(0, MAX_CHARS_PER_ITEM - 3) + "...";
        lines.push("- " + ctx);
      }
      return lines.join("\n");
    }

    var queryHex = encodeVector(queryEmb);
    var scored = [];
    for (var i = 0; i < lessons.length; i++) {
      var sim = cosineSimilarity(queryHex, lessons[i].vec);
      if (sim >= 0.15) {  // low threshold — lessons are generally useful
        scored.push({ mem: lessons[i], similarity: sim });
      }
    }
    if (scored.length === 0) return null;

    scored.sort(function(a, b) { return b.similarity - a.similarity; });
    if (scored.length > k) scored = scored.slice(0, k);

    var lines = ["## Known pitfalls"];
    for (var i = 0; i < scored.length; i++) {
      var ctx = scored[i].mem.context || scored[i].mem.name || "";
      if (ctx.length > MAX_CHARS_PER_ITEM) ctx = ctx.substring(0, MAX_CHARS_PER_ITEM - 3) + "...";
      lines.push("- " + ctx);
    }
    return lines.join("\n");
  }

  // ── Step 7: Boot State Singleton ──────────────────────────────────────

  function getOrCreateBootState() {
    try {
      // Search for an existing boot state object among all at_memory objects
      var objects = client.getObjects(typeKey);
      var bootObj = null;
      for (var i = 0; i < objects.length; i++) {
        var tags = getTagKeys(objects[i], "tag") || [];
        for (var ti = 0; ti < tags.length; ti++) {
          if (tags[ti] === "amemory_boot_state") {
            bootObj = objects[i];
            break;
          }
        }
        if (bootObj) break;
      }

      var state = { bootCount: 0, lastReflect: "", lastDecay: "", lastDigest: "" };

      if (bootObj) {
        // Found existing boot state — parse its body/markdown as JSON
        try {
          var fullObj = client.getObject(bootObj.id);
          var md = (fullObj && fullObj.markdown) ? fullObj.markdown : "";
          if (md) {
            var parsed = JSON.parse(md);
            if (parsed) {
              state.bootCount = typeof parsed.bootCount === "number" ? parsed.bootCount : 0;
              state.lastReflect = parsed.lastReflect || "";
              state.lastDecay = parsed.lastDecay || "";
              state.lastDigest = parsed.lastDigest || "";
            }
          }
        } catch (e) {
          // Parse failed — use defaults
        }
        // Increment bootCount
        state.bootCount = state.bootCount + 1;
        state._id = bootObj.id;
        // Update the object
        try {
          client.updateObject(bootObj.id, {
            markdown: JSON.stringify(state)
          });
        } catch (e) {}
      } else {
        // Create new boot state object
        state.bootCount = 1;
        try {
          // Create a minimal at_memory object for boot state
          // Needs a dummy vector to pass loadAllMemories filter, but boot state
          // is filtered out by tag anyway. Use a short placeholder.
          var result = client.createObject(typeKey, {
            name: "amemory_boot_state",
            body: JSON.stringify(state),
            properties: [
              { key: "__amemory_vector", text: "00" },
              { key: "__amemory_context", text: "Boot state singleton for amemory system" },
              { key: "__amemory_keywords", text: "boot,state,system" }
            ]
          });
          if (result.ok && result.object) {
            state._id = result.object.id;
            try { client.addTag(result.object.id, "amemory_boot_state"); } catch (e) {}
          }
        } catch (e) {}
      }

      return state;
    } catch (e) {
      // Best-effort — return defaults on any error
      return { bootCount: 1, lastReflect: "", lastDecay: "", lastDigest: "" };
    }
  }

  function updateBootState(state) {
    try {
      if (!state || !state._id) {
        // Try to find the boot state object
        var objects = client.getObjects(typeKey);
        for (var i = 0; i < objects.length; i++) {
          var tags = getTagKeys(objects[i], "tag") || [];
          for (var ti = 0; ti < tags.length; ti++) {
            if (tags[ti] === "amemory_boot_state") {
              state._id = objects[i].id;
              break;
            }
          }
          if (state._id) break;
        }
      }
      if (state._id) {
        // Serialize state without _id
        var serializable = {
          bootCount: state.bootCount,
          lastReflect: state.lastReflect,
          lastDecay: state.lastDecay,
          lastDigest: state.lastDigest
        };
        client.updateObject(state._id, {
          markdown: JSON.stringify(serializable)
        });
      }
    } catch (e) {
      // Best-effort — swallow errors
    }
  }

  // ── Step 8: Reflection System ─────────────────────────────────────────

  function reflect(bootState) {
    if (!bootState) {
      return { skipped: true, reason: "no bootState provided" };
    }

    // Gating: only run if bootCount % 3 === 0 AND 1h cooldown since lastReflect
    if (bootState.bootCount % 3 !== 0) {
      return { skipped: true, reason: "bootCount " + bootState.bootCount + " not divisible by 3" };
    }

    if (bootState.lastReflect) {
      try {
        var lastReflectMs = new Date(bootState.lastReflect).getTime();
        var nowMs = Date.now();
        var hourMs = 60 * 60 * 1000;
        if (!isNaN(lastReflectMs) && (nowMs - lastReflectMs) < hourMs) {
          return { skipped: true, reason: "cooldown: last reflect was " + Math.round((nowMs - lastReflectMs) / 60000) + " min ago (need 60)" };
        }
      } catch (e) {
        // If date parsing fails, proceed with reflection
      }
    }

    var insightsCreated = 0;
    var contradictionsFound = 0;

    // Load all memories once for both sub-operations
    var allMems = loadAllMemories(client, typeKey);

    // ── Sub-operation 1: Synthesis ──
    // Run if 5+ memories have accessCount === 0
    var unreflected = [];
    for (var i = 0; i < allMems.length; i++) {
      if (allMems[i].accessCount === 0) {
        unreflected.push(allMems[i]);
      }
    }

    if (unreflected.length >= 5) {
      // Take up to 15 unreflected memories
      var batch = unreflected.slice(0, 15);
      var memList = "";
      for (var bi = 0; bi < batch.length; bi++) {
        var m = batch[bi];
        var catLabel = (m.category || "claim").toUpperCase();
        memList += "  [" + catLabel + "] " + m.name + (m.context ? " -- " + m.context : "") + "\n";
      }

      var synthPrompt = "Given these memories, identify 1-2 patterns, generalizations, or mental models.\n\n"
        + "Memories:\n" + memList + "\n"
        + "Rules:\n"
        + "- Only output insights supported by 2+ memories above.\n"
        + "- Each insight must be a novel observation, not a restatement.\n"
        + "- Return JSON: {\"insights\": [{\"text\": \"...\", \"entities\": [\"entity1\"]}]}\n"
        + "- Return {\"insights\": []} if nothing emerges.\n\n"
        + "Output JSON ONLY:";

      try {
        var synthResp = llm.reason(synthPrompt);
        var synthParsed = llm.parseJSON(synthResp);
        if (synthParsed && synthParsed.insights && Array.isArray(synthParsed.insights)) {
          for (var si = 0; si < synthParsed.insights.length && si < 2; si++) {
            var insight = synthParsed.insights[si];
            if (!insight || !insight.text) continue;
            var insightEntities = [];
            if (insight.entities && Array.isArray(insight.entities)) {
              for (var iei = 0; iei < insight.entities.length; iei++) {
                if (typeof insight.entities[iei] === "string") {
                  insightEntities.push(insight.entities[iei].toLowerCase());
                }
              }
            }
            try {
              addMemory(insight.text, {
                category: "insight",
                entities: insightEntities
              });
              insightsCreated++;
            } catch (e) {}
          }
        }
      } catch (e) {
        // Synthesis LLM call failed — continue to contradiction detection
      }
    }

    // ── Sub-operation 2: Contradiction detection ──
    // Run if 2+ memories share entities
    var entityIndex = {}; // entity → [memory indices]
    for (var ei = 0; ei < allMems.length; ei++) {
      var entStr = allMems[ei].entities || "";
      if (!entStr) continue;
      var ents = entStr.split(",");
      for (var ej = 0; ej < ents.length; ej++) {
        var ent = ents[ej].trim().toLowerCase();
        if (!ent) continue;
        if (!entityIndex[ent]) entityIndex[ent] = [];
        entityIndex[ent].push(ei);
      }
    }

    // Find pairs sharing at least one entity
    var pairsSeen = {};
    var pairs = [];
    var entityKeys = Object.keys(entityIndex);
    for (var eki = 0; eki < entityKeys.length; eki++) {
      var members = entityIndex[entityKeys[eki]];
      if (members.length < 2) continue;
      for (var a = 0; a < members.length; a++) {
        for (var b = a + 1; b < members.length; b++) {
          var pk = members[a] + ":" + members[b];
          if (pairsSeen[pk]) continue;
          pairsSeen[pk] = true;
          pairs.push([allMems[members[a]], allMems[members[b]]]);
        }
      }
    }

    if (pairs.length >= 1) {
      // Cap at 10 pairs per run
      if (pairs.length > 10) pairs = pairs.slice(0, 10);

      for (var pi = 0; pi < pairs.length; pi++) {
        var memA = pairs[pi][0];
        var memB = pairs[pi][1];
        var contraPrompt = "Do these two memories contradict each other?\n\n"
          + "Memory A: " + memA.name + (memA.context ? " -- " + memA.context : "") + "\n"
          + "Memory B: " + memB.name + (memB.context ? " -- " + memB.context : "") + "\n\n"
          + "Return JSON: {\"contradicts\": true} or {\"contradicts\": false}\n"
          + "Only flag genuine logical contradictions. Different aspects of the same topic are NOT contradictions.\n"
          + "Output JSON ONLY:";

        try {
          var contraResp = llm.classify(contraPrompt);
          var contraParsed = llm.parseJSON(contraResp);
          if (contraParsed && contraParsed.contradicts === true) {
            contradictionsFound++;
            // Lower confidence of older memory by 2 (min 1)
            // Determine older by validFrom date
            var aDate = memA.validFrom || "";
            var bDate = memB.validFrom || "";
            var olderMem = (aDate <= bDate) ? memA : memB;
            var newConf = olderMem.confidence - 2;
            if (newConf < 1) newConf = 1;
            try {
              client.updateObject(olderMem.id, {
                properties: [{ key: "__amemory_confidence", number: newConf }]
              });
            } catch (e) {}

            // Add contradicts edge between them
            try {
              var aEdges = [];
              try { aEdges = JSON.parse(memA.edges); } catch (e) { aEdges = []; }
              if (!Array.isArray(aEdges)) aEdges = [];
              aEdges.push({ to: memB.id, type: "contradicts", strength: 0.8 });
              client.updateObject(memA.id, {
                properties: [{ key: "__amemory_edges", text: JSON.stringify(aEdges) }]
              });

              var bEdges = [];
              try { bEdges = JSON.parse(memB.edges); } catch (e) { bEdges = []; }
              if (!Array.isArray(bEdges)) bEdges = [];
              bEdges.push({ to: memA.id, type: "contradicts", strength: 0.8 });
              client.updateObject(memB.id, {
                properties: [{ key: "__amemory_edges", text: JSON.stringify(bEdges) }]
              });
            } catch (e) {}
          }
        } catch (e) {
          // Individual contradiction check failed — continue
        }
      }
    }

    // Update lastReflect in boot state
    bootState.lastReflect = new Date().toISOString();
    updateBootState(bootState);

    return { skipped: false, insights: insightsCreated, contradictions: contradictionsFound };
  }

  // ── Step 9: Salience Decay & Archival ─────────────────────────────────

  function decayMemories() {
    var decayed = 0;
    var archived = 0;
    var nowMs = Date.now();

    // Use the decay-specific loader that includes non-archived memories
    var allMems = loadAllMemoriesForDecay(client, typeKey);

    for (var i = 0; i < allMems.length; i++) {
      var mem = allMems[i];
      // Skip already archived
      if (mem.isArchived) continue;

      try {
        // Compute daysSince from validFrom or createdDate
        var refDate = mem.validFrom || "";
        if (!refDate) continue; // no date info — skip

        var refMs;
        try {
          refMs = new Date(refDate).getTime();
        } catch (e) {
          continue;
        }
        if (isNaN(refMs)) continue;

        var daysSince = (nowMs - refMs) / (24 * 60 * 60 * 1000);
        if (daysSince < 0) daysSince = 0;

        // Get decay rate for this category
        var decayRate = DECAY_RATES[mem.category] || DEFAULT_DECAY_RATE;

        // Apply protection rules
        // preference with confidence >= 8: skip decay (stable preference)
        if (mem.category === "preference" && mem.confidence >= 8) continue;
        // lesson with importance >= 8: skip decay (critical lesson)
        if (mem.category === "lesson" && mem.importance >= 8) continue;
        // decision with confidence >= 7: decay at 50% rate
        if (mem.category === "decision" && mem.confidence >= 7) {
          decayRate = decayRate * 0.5;
        }
        // taskstate with confidence > 5: skip decay (open task)
        if (mem.category === "taskstate" && mem.confidence > 5) continue;

        // Compute new salience
        var newSalience = mem.salience - (daysSince * decayRate);
        if (newSalience < 0) newSalience = 0;

        // Only update if salience actually changed (avoid unnecessary API calls)
        if (Math.abs(newSalience - mem.salience) < 0.01) continue;

        // Check for archival: salience < 0.5 AND never accessed AND 30+ days old
        if (newSalience < 0.5 && mem.accessCount === 0 && daysSince > 30) {
          // Soft archive — add amemory_archived tag
          try {
            client.addTag(mem.id, "amemory_archived");
            client.updateObject(mem.id, {
              properties: [{ key: "__amemory_salience", number: newSalience }]
            });
            archived++;
          } catch (e) {}
          continue;
        }

        // Update salience
        client.updateObject(mem.id, {
          properties: [{ key: "__amemory_salience", number: Math.round(newSalience * 100) / 100 }]
        });
        decayed++;
      } catch (e) {
        // Best-effort: don't fail the whole batch on individual errors
      }
    }

    return { decayed: decayed, archived: archived };
  }

  // ── Chat-chunk memories: compressed conversation history with a period span ──
  //
  // A "chat chunk" is a summarized slice of chat history covering a time period.
  // It's a first-class at_memory (runs through addMemory's metadata/embed/link
  // pipeline for semantic recall), but is additionally tagged `amemory_chat_chunk`
  // and carries two date properties marking the span it covers.

  function createChatChunk(opts) {
    opts = opts || {};
    var periodStart = opts.period_start || opts.periodStart;
    var periodEnd = opts.period_end || opts.periodEnd;
    var summary = opts.summary;
    if (!summary || !periodStart || !periodEnd) {
      return { ok: false, error: "createChatChunk requires summary, period_start, period_end" };
    }

    // chat_chunk is a first-class category (slice 1b). Use addMemory's
    // skip-classifier branch — the summary IS already a Sonnet-produced
    // dense paragraph, running it through the Haiku classifier adds nothing.
    // context = first sentence of the summary (the natural one-liner).
    var firstSentenceMatch = summary.match(/^[^.\n!?]+[.!?\n]/);
    var chunkContext = firstSentenceMatch ? firstSentenceMatch[0].trim() : summary.substring(0, 160);
    var memOpts = {
      category: "chat_chunk",
      context: chunkContext
    };
    if (typeof opts.turns_covered === "number") {
      memOpts.importance = Math.max(1, Math.min(10, Math.round(3 + Math.log(opts.turns_covered + 1))));
    }

    var added = addMemory(summary, memOpts);
    if (!added || !added.ok) return added;

    // Patch the period properties onto the object. These were not part of the
    // addMemory pipeline, so they go in as a secondary updateObject call.
    // The Anytype API rejects ISO strings without a timezone designator —
    // coerce missing-timezone input by appending "Z" (UTC assumption) so
    // callers that pass "2026-04-16T10:00:00" don't silently lose their period.
    function _withTZ(s) {
      if (typeof s !== "string") return s;
      if (/Z$/.test(s)) return s;
      if (/[+\-]\d{2}:?\d{2}$/.test(s)) return s;
      return s + "Z";
    }
    var normStart = _withTZ(periodStart);
    var normEnd = _withTZ(periodEnd);
    var chunkChatId = (typeof opts.chatId === "string" && opts.chatId.length > 0) ? opts.chatId : "";
    var updateErr = null;
    try {
      var patchProps = [
        { key: "__amemory_period_start", date: normStart },
        { key: "__amemory_period_end", date: normEnd }
      ];
      if (chunkChatId) patchProps.push({ key: "__amemory_chat_id", text: chunkChatId });
      var upd = client.updateObject(added.id, { properties: patchProps });
      if (upd && upd.ok === false) updateErr = upd.error || "updateObject returned ok:false";
    } catch (e) {
      updateErr = (e && e.message) ? e.message : String(e);
    }
    if (updateErr) {
      return { ok: false, id: added.id, error: "chunk created but period properties failed: " + updateErr };
    }

    return { ok: true, id: added.id, period_start: normStart, period_end: normEnd, chatId: chunkChatId };
  }

  // searchByPeriod(from, until, opts) — return chat chunks whose [start, end]
  // overlaps the requested [from, until]. Overlap semantics:
  //   periodEnd >= from  &&  periodStart <= until
  // So a chunk that straddles either boundary is returned. Sorted by
  // periodStart ascending (chronological). opts.k caps the result size;
  // opts.k = 0 or undefined returns all matching chunks.
  //
  // Slice 1b: also includes non-chunk memories whose createdDate (or validFrom
  // fallback) falls inside [from, until]. Non-chunks are returned alongside
  // chunks in the unified `chunks` array — each entry carries `kind` so the
  // caller can distinguish. (The field name stays `chunks` for backward
  // compatibility with callers that drop into `res.chunks`.)
  function searchByPeriod(from, until, opts) {
    opts = opts || {};
    var k = opts.k || 0;
    if (!from || !until) {
      return { ok: false, error: "searchByPeriod requires from and until ISO timestamps" };
    }

    var allMems = loadAllMemories(client, typeKey);
    var catFilter = _buildCategoryFilter(opts.categories, opts.excludeCategories);
    var chatFilter = _buildChatIdFilter(opts.chatId, opts.chatIdFilter);
    var results = [];
    for (var i = 0; i < allMems.length; i++) {
      var m = allMems[i];
      if (catFilter && !catFilter(m)) continue;
      if (chatFilter && !chatFilter(m)) continue;
      var isChunk = m.tags.indexOf("chat_chunk") !== -1;
      if (isChunk) {
        if (!m.periodStart || !m.periodEnd) continue;
        if (m.periodEnd < from) continue;
        if (m.periodStart > until) continue;
        results.push({
          id: m.id, name: m.name, context: m.context, kind: "chat_chunk",
          period_start: m.periodStart, period_end: m.periodEnd,
          sort_key: m.periodStart,
          keywords: m.keywords, tags: m.tags, chatId: m.chatId || "", obj: m.obj
        });
      } else {
        var when = m.validFrom || m.createdDate || "";
        if (!when) continue;
        if (when < from || when > until) continue;
        results.push({
          id: m.id, name: m.name, context: m.context, kind: m.category || "memory",
          period_start: when, period_end: when,
          sort_key: when,
          keywords: m.keywords, tags: m.tags, chatId: m.chatId || "", obj: m.obj
        });
      }
    }
    results.sort(function(a, b) {
      if (a.sort_key < b.sort_key) return -1;
      if (a.sort_key > b.sort_key) return 1;
      return 0;
    });
    for (var ri = 0; ri < results.length; ri++) delete results[ri].sort_key;
    if (k > 0 && results.length > k) results = results.slice(0, k);
    return { ok: true, chunks: results };
  }

  // listChatChunks — paginate compressed chat history chunks.
  // opts: { offset?, limit?, periodFrom?, periodUntil?, order? }
  //   offset (default 0), limit (default 10, max 50),
  //   periodFrom/periodUntil (ISO strings) narrow to overlapping chunks,
  //   order "newest_first" (default) or "oldest_first".
  // Returns { ok, total, offset, limit, order, chunks: [{id, name, context,
  // period_start, period_end, keywords, tags}] }. Bodies are NOT fetched —
  // callers that need the markdown should hit anyHelper.getObject(id).
  function listChatChunks(opts) {
    opts = opts || {};
    var offset = opts.offset || 0;
    var limit = opts.limit || 10;
    if (limit > 50) limit = 50;
    var order = opts.order || "newest_first";

    var allMems = loadAllMemories(client, typeKey);
    var chatFilter = _buildChatIdFilter(opts.chatId, opts.chatIdFilter);
    var chunks = [];
    for (var i = 0; i < allMems.length; i++) {
      var m = allMems[i];
      if (m.tags.indexOf("chat_chunk") === -1) continue;
      if (!m.periodStart || !m.periodEnd) continue;
      if (chatFilter && !chatFilter(m)) continue;
      if (opts.periodFrom && m.periodEnd < opts.periodFrom) continue;
      if (opts.periodUntil && m.periodStart > opts.periodUntil) continue;
      chunks.push(m);
    }
    chunks.sort(function(a, b) {
      if (a.periodStart < b.periodStart) return -1;
      if (a.periodStart > b.periodStart) return 1;
      return 0;
    });
    if (order === "newest_first") chunks.reverse();

    var total = chunks.length;
    var sliced = chunks.slice(offset, offset + limit);
    var out = sliced.map(function(m) {
      return {
        id: m.id, name: m.name, context: m.context,
        period_start: m.periodStart, period_end: m.periodEnd,
        keywords: m.keywords, tags: m.tags, chatId: m.chatId || ""
      };
    });
    return { ok: true, total: total, offset: offset, limit: limit, order: order, chunks: out };
  }

  // getChunkNeighbours — return the chunks temporally adjacent to a given
  // chat_chunk. "Before" = chunks whose periodEnd precedes target.periodStart,
  // closest N first. "After" = chunks whose periodStart follows target.periodEnd,
  // closest N first. Overlapping chunks are excluded. Used by search_memory to
  // expand a retrieved set with its temporal context before reranking.
  function getChunkNeighbours(chunkId, opts) {
    opts = opts || {};
    var before = opts.before != null ? opts.before : 1;
    var after = opts.after != null ? opts.after : 1;
    if (!chunkId) return { ok: false, error: "chunkId required" };

    var allMems = loadAllMemories(client, typeKey);
    var target = null;
    var allChunks = [];
    for (var i = 0; i < allMems.length; i++) {
      var m = allMems[i];
      if (m.tags.indexOf("chat_chunk") === -1) continue;
      if (!m.periodStart || !m.periodEnd) continue;
      if (m.id === chunkId) target = m;
      allChunks.push(m);
    }
    if (!target) return { ok: false, error: "chunk not found or missing period: " + chunkId };

    var beforeChunks = [];
    var afterChunks = [];
    for (var j = 0; j < allChunks.length; j++) {
      var c = allChunks[j];
      if (c.id === chunkId) continue;
      if (c.periodEnd < target.periodStart) beforeChunks.push(c);
      else if (c.periodStart > target.periodEnd) afterChunks.push(c);
    }
    beforeChunks.sort(function(a, b) {
      if (a.periodEnd < b.periodEnd) return -1;
      if (a.periodEnd > b.periodEnd) return 1;
      return 0;
    });
    afterChunks.sort(function(a, b) {
      if (a.periodStart < b.periodStart) return -1;
      if (a.periodStart > b.periodStart) return 1;
      return 0;
    });
    var beforeSlice = beforeChunks.slice(Math.max(0, beforeChunks.length - before));
    var afterSlice = afterChunks.slice(0, after);

    function toOut(m) {
      return {
        id: m.id,
        name: m.name,
        context: m.context,
        period_start: m.periodStart,
        period_end: m.periodEnd,
        keywords: m.keywords,
        tags: m.tags
      };
    }
    return { ok: true, target: toOut(target), before: beforeSlice.map(toOut), after: afterSlice.map(toOut) };
  }

  // Convenience for "give me the last N chunks regardless of range" — used at
  // boot to prepend the most recent compressed context. Fetches full objects
  // (with markdown bodies) for the N most recent chunks, since loadAllMemories
  // returns partial objects without markdown.
  function getRecentChatChunks(n, opts) {
    // Back-compat: accept a plain number or { n, chatId, chatIdFilter }.
    if (typeof n === "object" && n !== null) {
      opts = n;
      n = opts.n;
    }
    n = n || 2;
    opts = opts || {};
    var chatFilter = _buildChatIdFilter(opts.chatId, opts.chatIdFilter);
    var allMems = loadAllMemories(client, typeKey);
    var chunks = [];
    for (var i = 0; i < allMems.length; i++) {
      var m = allMems[i];
      if (m.tags.indexOf("chat_chunk") === -1) continue;
      if (!m.periodEnd) continue;
      if (chatFilter && !chatFilter(m)) continue;
      chunks.push({
        id: m.id,
        name: m.name,
        context: m.context,
        period_start: m.periodStart,
        period_end: m.periodEnd,
        keywords: m.keywords,
        tags: m.tags,
        chatId: m.chatId || ""
      });
    }
    chunks.sort(function(a, b) {
      if (a.period_end < b.period_end) return 1;
      if (a.period_end > b.period_end) return -1;
      return 0;
    });
    if (chunks.length > n) chunks = chunks.slice(0, n);

    // Now fetch full markdown for each selected chunk.
    for (var fi = 0; fi < chunks.length; fi++) {
      try {
        var full = client.getObject(chunks[fi].id);
        chunks[fi].body = (full && full.markdown) || "";
      } catch (e) {
        chunks[fi].body = "";
      }
    }

    chunks.reverse(); // oldest-first for chronological rendering
    return chunks;
  }

  return {
    addMemory: addMemory,
    searchMemories: searchMemories,
    rewriteQuery: rewriteQuery,
    searchWithRewrite: searchWithRewrite,
    getContext: getContext,
    getClusteredContext: getClusteredContext,
    getPreferencesAndLessons: getPreferencesAndLessons,
    getSessionContext: getSessionContext,
    getCodegenLessons: getCodegenLessons,
    getCategories: getCategories,
    listCategories: listCategories,
    getStats: getStats,
    verifyMemory: verifyMemory,
    verifyAll: verifyAll,
    rethink: rethink,
    detectTemporalReference: detectTemporalReference,
    // Step 7: Boot state management
    getOrCreateBootState: getOrCreateBootState,
    updateBootState: updateBootState,
    // Step 8: Reflection system
    reflect: reflect,
    // Step 9: Salience decay & archival
    decayMemories: decayMemories,
    // Chat-chunk period memory
    createChatChunk: createChatChunk,
    searchByPeriod: searchByPeriod,
    listChatChunks: listChatChunks,
    getChunkNeighbours: getChunkNeighbours,
    getRecentChatChunks: getRecentChatChunks
  };
}

// ── Agent-facing tool surface ────────────────────────────────────────────────
//
// Kernel-global entry points. When this module is tagged any_tool, the
// toolcall_core facade binds these exports on the `amemory` global so agents
// can call them from run_cell without any wiring. Narrow by design — heavier
// instance-only functions (reflect, decay, evolution, verifyMemory, rethink,
// extractInsights-style auto-harvesting) stay on the createAMemory factory
// and are not surfaced here.

var _toolAmem = null;

function _getToolAmem() {
  if (_toolAmem) return _toolAmem;
  // noTrace: skip __wrapTrace on the internal client so amemory's helper
  // reads/writes stay invisible to the agent-visible trace. The kernel-global
  // anyHelper facade the agent uses directly is a separate instance and
  // keeps its normal wrap — agent's own calls trace cleanly.
  var client = createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID,
    noTrace: true
  });
  _toolAmem = createAMemory(client, { enableLinks: false });
  return _toolAmem;
}

function _compactChunk(c) {
  return {
    id: c.id,
    name: c.name,
    kind: "chat_chunk",
    period: { start: c.period_start, end: c.period_end },
    context: c.context,
    keywords: c.keywords,
    tags: c.tags,
    chatId: c.chatId || ""
  };
}

function _compactPeriodItem(item) {
  return {
    id: item.id,
    name: item.name,
    kind: item.kind || "memory",
    period: { start: item.period_start, end: item.period_end },
    context: item.context,
    keywords: item.keywords,
    tags: item.tags,
    chatId: item.chatId || ""
  };
}

function _compactResult(r) {
  var tags = r.tags || [];
  var isChunk = tags.indexOf && tags.indexOf("chat_chunk") !== -1;
  return {
    id: r.id,
    name: r.name,
    kind: isChunk ? "chat_chunk" : (r.category || "memory"),
    context: r.context,
    keywords: r.keywords,
    tags: tags,
    chatId: r.chatId || "",
    score: r.similarity,
    viaLink: !!r.viaLink
  };
}

// search — agent-facing hybrid search. See amemory@v2.md for full contract.
// Dispatches across: explicit period slice, auto-detected temporal phrase,
// and semantic+rewrite fallback. Returns compact results; caller fetches full
// body via anyHelper.getObject(id).
function _searchImpl(query, opts) {
  opts = opts || {};
  var k = opts.k || 8;
  if (k > 20) k = 20;
  var amem = _getToolAmem();
  // Category filter — threaded through every dispatch mode. Silent fallback:
  // agents sometimes pass addMemory-shape `{category: "X"}` on a search.
  // Promote singular to an array so both shapes work. `categories` wins when
  // both are present (it's the richer filter).
  var cats = opts.categories;
  if (!Array.isArray(cats) && typeof opts.category === "string" && opts.category.length > 0) {
    cats = [opts.category];
  }
  var filterOpts = {
    categories: cats,
    excludeCategories: opts.excludeCategories,
    chatId: opts.chatId,
    chatIdFilter: opts.chatIdFilter
  };

  if (opts.periodFrom && opts.periodUntil) {
    var periodOpts = { k: k };
    periodOpts.categories = filterOpts.categories;
    periodOpts.excludeCategories = filterOpts.excludeCategories;
    periodOpts.chatId = filterOpts.chatId;
    periodOpts.chatIdFilter = filterOpts.chatIdFilter;
    var res = amem.searchByPeriod(opts.periodFrom, opts.periodUntil, periodOpts);
    if (!res.ok) return res;
    return { ok: true, mode: "period", results: res.chunks.map(_compactPeriodItem) };
  }

  if (query) {
    var win = amem.detectTemporalReference(query);
    if (win && win.from && win.until) {
      var periodOpts2 = { k: k };
      periodOpts2.categories = filterOpts.categories;
      periodOpts2.excludeCategories = filterOpts.excludeCategories;
      periodOpts2.chatId = filterOpts.chatId;
      periodOpts2.chatIdFilter = filterOpts.chatIdFilter;
      var res2 = amem.searchByPeriod(win.from, win.until, periodOpts2);
      if (!res2.ok) return res2;
      return { ok: true, mode: "period_detected", detected: win, results: res2.chunks.map(_compactPeriodItem) };
    }
  }

  if (!query) return { ok: false, error: "query is required when no period range is provided" };
  var sr = amem.searchWithRewrite(query, k, null, filterOpts);
  var results = (sr && sr.results) || [];
  return { ok: true, mode: "semantic", results: results.map(_compactResult), rewrite: sr && sr.rewrite };
}

// Paginated chronological browse of compressed chat history (chunks only).
// Top-level export; doesn't shadow the createAMemory-internal listChatChunks
// method (that's closure-scoped and only reachable via createAMemory(client)).
function _listChatChunksImpl(opts) {
  var amem = _getToolAmem();
  var res = amem.listChatChunks(opts || {});
  if (!res.ok) return res;
  return {
    ok: true,
    total: res.total,
    offset: res.offset,
    limit: res.limit,
    order: res.order,
    chunks: res.chunks.map(_compactChunk)
  };
}

// ±N temporal neighbours of a chat_chunk.
function _getNeighboursImpl(chunkId, opts) {
  var amem = _getToolAmem();
  var res = amem.getChunkNeighbours(chunkId, opts || {});
  if (!res.ok) return res;
  return {
    ok: true,
    target: _compactChunk(res.target),
    before: res.before.map(_compactChunk),
    after: res.after.map(_compactChunk)
  };
}

// Agent-judged memory save. Validates required opts, then delegates to the
// singleton's addMemory (with opts.category and opts.context supplied, that
// takes the skip-classifier path).
function _addMemoryImpl(text, opts) {
  if (!text || typeof text !== "string") {
    return { ok: false, error: "text is required (string)" };
  }
  opts = opts || {};
  // Silent fallback: agents sometimes pass search-shape `{categories: ["X"]}`
  // on a write. Promote the first element to `category` when the canonical
  // singular key is missing. `category` wins if both are present.
  if (!opts.category && Array.isArray(opts.categories) && opts.categories.length > 0 && typeof opts.categories[0] === "string") {
    opts = Object.assign({}, opts, { category: opts.categories[0] });
  }
  if (!opts.category || typeof opts.category !== "string") {
    return { ok: false, error: "opts.category is required (string)" };
  }
  if (!opts.context || typeof opts.context !== "string") {
    return { ok: false, error: "opts.context is required (one-line summary string)" };
  }
  return _getToolAmem().addMemory(text, opts);
}

// { builtin, observed } category inspection.
function _listCategoriesImpl() {
  return _getToolAmem().listCategories();
}

// __wrapTrace each public method so the agent-visible trace shows one clean
// "amemory.X(args) → result" entry per call instead of the dozen fetch /
// anyHelper sub-entries the wrapped impl triggers internally. Follows the
// same pattern anyHelper uses (anyHelper.js:2104).
var _w = (typeof __wrapTrace === "function")
  ? function(name, fn) { return __wrapTrace("amemory." + name, fn); }
  : function(_name, fn) { return fn; };

export var search = _w("search", _searchImpl);
export var listChatChunks = _w("listChatChunks", _listChatChunksImpl);
export var getNeighbours = _w("getNeighbours", _getNeighboursImpl);
export var addMemory = _w("addMemory", _addMemoryImpl);
export var listCategories = _w("listCategories", _listCategoriesImpl);

// Internal hosts amemory calls directly (LLM providers for classification /
// rewrite / embeddings). Fetches to these are pure noise in the agent-visible
// trace — the wrapped "amemory.X" entry already captures the meaningful
// input → output.
// Every LLM/embedding provider host amemory may route through, synced with
// llm.js (classify/summarize/codegen/reason/converse + embeddings). When a
// fetchBatch contains only URLs to these hosts, it's amemory-internal noise
// and gets dropped from the agent-visible trace.
var INTERNAL_HOSTS = [
  "https://api.anthropic.com",
  "https://api.openai.com",
  "https://openrouter.ai",
  "https://api.together.xyz",
  "https://api.together.ai",
  "https://api.groq.com",
  "https://api.moonshot.ai",
  "https://generativelanguage.googleapis.com"
];

function _isInternalHost(url) {
  if (!url) return false;
  for (var i = 0; i < INTERNAL_HOSTS.length; i++) {
    if (url.indexOf(INTERNAL_HOSTS[i]) === 0) return true;
  }
  return false;
}

function _extractFetchUrl(input) {
  try {
    var parsed = JSON.parse(input);
    if (Array.isArray(parsed) && typeof parsed[0] === "string") return parsed[0];
    if (typeof parsed === "string") return parsed;
  } catch (e) {}
  return "";
}

function _extractBatchUrls(input) {
  try {
    var parsed = JSON.parse(input);
    if (!Array.isArray(parsed) || parsed.length === 0) return [];
    var batchArg = parsed[0];
    if (!Array.isArray(batchArg)) return [];
    var urls = [];
    for (var i = 0; i < batchArg.length; i++) {
      var pair = batchArg[i];
      if (Array.isArray(pair) && typeof pair[0] === "string") urls.push(pair[0]);
      else if (typeof pair === "string") urls.push(pair);
    }
    return urls;
  } catch (e) { return []; }
}

// Per-tool trace-cleanup hook. toolcall_core chains this across all tools
// during tool_result prep (toolcall_core@v1.js:263). Drops fetch / fetchBatch
// entries whose URLs are all to LLM provider hosts (amemory-internal, never
// called by the agent directly). Helper internals are handled by the client
// being built with noTrace:true — they never enter the trace in the first
// place, so no anyHelper.* filter needed here.
export function __prepareTraces(traces) {
  if (!traces) return traces;
  var out = {};
  for (var k in traces) {
    if (!traces.hasOwnProperty(k)) continue;

    if (k === "fetch") {
      var keptFetch = {};
      var anyFetch = false;
      for (var input in traces.fetch) {
        if (!traces.fetch.hasOwnProperty(input)) continue;
        if (_isInternalHost(_extractFetchUrl(input))) continue;
        keptFetch[input] = traces.fetch[input];
        anyFetch = true;
      }
      if (anyFetch) out.fetch = keptFetch;
      continue;
    }

    if (k === "fetchBatch") {
      var keptBatch = {};
      var anyBatch = false;
      for (var input2 in traces.fetchBatch) {
        if (!traces.fetchBatch.hasOwnProperty(input2)) continue;
        var urls = _extractBatchUrls(input2);
        var allInternal = urls.length > 0;
        for (var u = 0; u < urls.length; u++) {
          if (!_isInternalHost(urls[u])) { allInternal = false; break; }
        }
        if (allInternal) continue;
        keptBatch[input2] = traces.fetchBatch[input2];
        anyBatch = true;
      }
      if (anyBatch) out.fetchBatch = keptBatch;
      continue;
    }

    out[k] = traces[k];
  }
  return out;
}

// ── Module main (for testing + program-run entry point) ──────────────────────

export function main(args) {
  args = args || {};
  var action = args.action;
  if (!action) {
    return JSON.stringify({ module: "amemory@v2", status: "loaded", features: ["Ps1-metadata", "Ps2-links", "Ps2-typed-edges", "Ps3-evolution", "Ps4-verify", "Ps5-rethink", "minSimilarity", "graph-retrieval", "clustered-context", "hybrid-fts", "temporal-recency", "temporal-decay", "content-dedup", "structured-properties", "category-tags", "entity-extraction", "entity-recall", "edge-traversal", "multi-dim-scoring", "access-count-tracking", "pref-lesson-injection", "boot-state", "reflection", "salience-decay", "archival", "tool-surface"] });
  }
  if (action === "search") return JSON.stringify(search(args.query || "", args));
  if (action === "listChatChunks") return JSON.stringify(listChatChunks(args));
  if (action === "getNeighbours") return JSON.stringify(getNeighbours(args.chunkId, args));
  if (action === "addMemory") return JSON.stringify(addMemory(args.text || "", args));
  if (action === "listCategories") return JSON.stringify(listCategories());
  return JSON.stringify({ ok: false, error: "unknown action: " + action });
}
