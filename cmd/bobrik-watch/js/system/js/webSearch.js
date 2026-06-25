// __main_source
// Web Search tool — minimalistic grounded search via Gemini API + Google Search.
// Accepts one or more queries (variadic), runs them in parallel, returns each as a
// single formatted string for easy LLM consumption. Each query yields ONE hit:
// the Gemini-synthesized answer with its grounding sources listed inline.

import { main as getConfig } from "config@v1";

var GEMINI_MODEL = "gemini-2.5-flash";
var GEMINI_BASE = "https://generativelanguage.googleapis.com/v1beta/models/";

function _systemPrompt() {
  return "You are a web-search backend. Given a query, search the web and answer in 4-8 sentences. " +
    "Be concrete: cite numbers, dates, names, versions. Do not add preamble or hedging — just the answer.";
}

function _buildFetchArgs(geminiKey, model, query) {
  var url = GEMINI_BASE + model + ":generateContent";
  return [url, {
    method: "POST",
    headers: {
      "x-goog-api-key": geminiKey,
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      system_instruction: { parts: [{ text: _systemPrompt() }] },
      contents: [{ role: "user", parts: [{ text: query }] }],
      tools: [{ google_search: {} }]
    })
  }];
}

function _parseResp(resp) {
  if (!resp || !resp.ok) {
    var errBody = resp && resp.body;
    var errMsg = (errBody && errBody.error && errBody.error.message)
      ? errBody.error.message
      : "HTTP " + (resp ? resp.status : "unknown");
    return { ok: false, error: errMsg };
  }
  var data = resp.body;
  var candidate = data.candidates && data.candidates[0];
  if (!candidate || !candidate.content || !candidate.content.parts) {
    return { ok: false, error: "empty response from Gemini" };
  }
  var answer = "";
  for (var i = 0; i < candidate.content.parts.length; i++) {
    if (candidate.content.parts[i].text) answer += candidate.content.parts[i].text;
  }
  var sources = [];
  var gm = candidate.groundingMetadata;
  if (gm && gm.groundingChunks) {
    var seen = {};
    for (var ci = 0; ci < gm.groundingChunks.length; ci++) {
      var c = gm.groundingChunks[ci];
      if (c.web && c.web.uri && !seen[c.web.uri]) {
        seen[c.web.uri] = true;
        sources.push({ url: c.web.uri, title: c.web.title || c.web.domain || "" });
      }
    }
  }
  return { ok: true, answer: answer, sources: sources };
}

function _formatResult(idx, query, parsed) {
  var primaryUrl = parsed.sources.length > 0 ? parsed.sources[0].url : "";
  var lines = ["[" + idx + "] " + query, primaryUrl, "", parsed.answer];
  if (parsed.sources.length > 1) {
    lines.push("");
    lines.push("Sources:");
    for (var i = 1; i < parsed.sources.length; i++) {
      var s = parsed.sources[i];
      lines.push("- " + (s.title || s.url) + " — " + s.url);
    }
  }
  return lines.join("\n");
}

function _formatError(idx, query, err) {
  return "[ERROR] query " + idx + " (\"" + query + "\") failed: " + err;
}

// Extract the final URL from a HEAD + redirect:manual response. The runtime
// (fetch.go) returns the 302 intact when redirect:manual is set: status is the
// 3xx code and the Location header holds the target. If the option was
// ignored (older runtime) the auto-followed case is covered by resp.url.
function _finalUrlOf(resp, fallback) {
  if (!resp) return fallback;
  if (resp.headers) {
    var loc = resp.headers.location || resp.headers.Location;
    if (typeof loc === "string" && loc) return loc;
  }
  if (typeof resp.url === "string" && resp.url) return resp.url;
  return fallback;
}

// Replace Gemini's vertex grounding-redirect URLs with the target URLs each
// one resolves to. Uses HEAD + redirect:"manual" so we only talk to Google's
// redirect endpoint — never hit the destination server (no UA gating, consent
// walls, or body download). Collects every unique source URL across all query
// parses, fetches them in one parallel batch, builds a redirect→final map,
// then rewrites source.url in place. Keeps original URL if resolution fails.
function _resolveSourceRedirects(parsedResponses) {
  var uniqueUrls = [];
  var seen = {};
  for (var ri = 0; ri < parsedResponses.length; ri++) {
    var p = parsedResponses[ri];
    if (!p || !p.ok) continue;
    for (var si = 0; si < p.sources.length; si++) {
      var u = p.sources[si] && p.sources[si].url;
      if (u && !seen[u]) { seen[u] = true; uniqueUrls.push(u); }
    }
  }
  if (uniqueUrls.length === 0) return;

  var fetchArgs = [];
  for (var fi = 0; fi < uniqueUrls.length; fi++) {
    fetchArgs.push([uniqueUrls[fi], { method: "HEAD", redirect: "manual" }]);
  }
  var resolveResps;
  try { resolveResps = fetchBatch(fetchArgs); } catch (e) { return; }

  var urlMap = {};
  for (var mi = 0; mi < uniqueUrls.length; mi++) {
    urlMap[uniqueUrls[mi]] = _finalUrlOf(resolveResps[mi], uniqueUrls[mi]);
  }
  for (var pi = 0; pi < parsedResponses.length; pi++) {
    var pp = parsedResponses[pi];
    if (!pp || !pp.ok) continue;
    for (var psi = 0; psi < pp.sources.length; psi++) {
      var orig = pp.sources[psi].url;
      if (orig && urlMap[orig]) pp.sources[psi].url = urlMap[orig];
    }
  }
}

function _searchImpl() {
  var config = getConfig();
  var geminiKey = config.GEMINI_API_KEY;
  if (!geminiKey) {
    return ["[ERROR] GEMINI_API_KEY not set in config@v1"];
  }

  // Leniency: search([q1, q2]) works the same as search(q1, q2). The variadic
  // form is the documented contract, but JS-idiomatic "array of queries" is
  // the natural mistake and cheap to accept.
  var rawArgs = arguments;
  if (arguments.length === 1 && Array.isArray(arguments[0])) rawArgs = arguments[0];

  var queries = [];
  for (var i = 0; i < rawArgs.length; i++) {
    var arg = rawArgs[i];
    if (typeof arg === "string") queries.push(arg);
    else if (arg && arg.query) queries.push(arg.query);
  }
  if (queries.length === 0) return [];

  var fetchArgs = [];
  for (var qi = 0; qi < queries.length; qi++) {
    fetchArgs.push(_buildFetchArgs(geminiKey, GEMINI_MODEL, queries[qi]));
  }
  var responses = fetchBatch(fetchArgs);

  // Parse all responses first so we can batch-resolve source redirects across
  // ALL queries in one parallel fetchBatch.
  var parsedAll = [];
  for (var pri = 0; pri < responses.length; pri++) {
    parsedAll.push(_parseResp(responses[pri]));
  }
  _resolveSourceRedirects(parsedAll);

  var out = [];
  for (var ri = 0; ri < parsedAll.length; ri++) {
    var parsed = parsedAll[ri];
    if (parsed.ok) {
      out.push(_formatResult(out.length + 1, queries[ri], parsed));
    } else {
      out.push(_formatError(ri + 1, queries[ri], parsed.error));
    }
  }
  return out;
}

// Wrap search via __wrapTrace so Effects shows `webSearch.search(...) → [...]`
// instead of leaving only the inner fetchBatch traces as the visible signal.
// Falls back to the raw impl if the runtime doesn't expose __wrapTrace.
export var search = (typeof __wrapTrace === "function")
  ? __wrapTrace("webSearch.search", _searchImpl)
  : _searchImpl;

// Hosts this tool calls internally (Gemini synth + Vertex redirect resolution).
// __prepareTraces below drops fetchBatch entries whose URLs all land on these
// hosts — they're implementation detail, and the model already sees the
// semantic `webSearch.search` trace entry.
var INTERNAL_HOSTS = [
  "https://generativelanguage.googleapis.com",
  "https://vertexaisearch.cloud.google.com"
];

function _isInternalHost(url) {
  if (!url) return false;
  for (var i = 0; i < INTERNAL_HOSTS.length; i++) {
    if (url.indexOf(INTERNAL_HOSTS[i]) === 0) return true;
  }
  return false;
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

// Per-tool trace-cleanup hook. toolcall_core discovers this on each tool
// facade and chains them during tool_result prep. Keep it cheap and local —
// the whole point is that toolcall_core doesn't need to know about Gemini.
export function __prepareTraces(traces) {
  if (!traces || !traces.fetchBatch) return traces;
  var kept = {};
  var any = false;
  for (var input in traces.fetchBatch) {
    if (!traces.fetchBatch.hasOwnProperty(input)) continue;
    var urls = _extractBatchUrls(input);
    var allInternal = urls.length > 0;
    for (var i = 0; i < urls.length; i++) {
      if (!_isInternalHost(urls[i])) { allInternal = false; break; }
    }
    if (allInternal) continue;
    kept[input] = traces.fetchBatch[input];
    any = true;
  }
  var out = {};
  for (var k in traces) {
    if (k === "fetchBatch") continue;
    out[k] = traces[k];
  }
  if (any) out.fetchBatch = kept;
  return out;
}

export function main(args) {
  if (args && args.query) return search(args.query);
  return search();
}
