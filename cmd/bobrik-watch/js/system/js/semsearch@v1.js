// __main_source
// semsearch@v1 — CHEAP semantic + full-text search over the server's local
// index (BM25 lexical + vector embeddings; pipeline: docs/13-index.md). One
// HTTP call to POST /v1/spaces/:spaceId/search — NO inner LLM loop, no token
// burn, ~milliseconds. This is the search tool to reach for FIRST.
//
// Contrast the RLM `search`/`ask` program (programs/search@v1.js,
// docs/12-rlm-search.md): that one spins an isolated inner LLM loop that pages
// the datasets and maps batched sub-LLM relevance calls over snippets —
// expensive (seconds, real tokens) but able to reason, synthesize an answer,
// and scan records the index does not cover. Escalate to it only when semsearch
// comes back thin or you need a grounded synthesized answer.
//
// Cross-space: pass opts.space (any space id on the account) to search a space
// other than the agent's own — same paradigm as every anyHelper method. The
// thin wrapper just delegates to anyHelper.search so the cross-space mapping
// (_resolveSpaceId) and error envelope are shared, single-sourced.

import { createClient } from "anyHelper@v1";

function _newClient() {
  return createClient({
    apiBaseUrl: env.ANYTYPE_API_URL,
    apiKey: env.ANYTYPE_API_KEY,
    spaceId: env.ANYTYPE_SPACE_ID,
    noTrace: true
  });
}

// Each hit's `data` is a ranking/recognition PREVIEW (the doc calls it "the
// short indexed text, not full records — hydrate via getObjects"). We trim the
// result so even a WORST-CASE default-limit reply (every data at the cap,
// chat hits with 59-char CID recordIds, model pretty-prints the whole thing)
// renders inline in the run_cell kernel instead of tripping the >4000-char
// value-store stub — the stub forces a logs.get round-trip that bites the
// model when it has logged a stringified copy. Three knobs, sized together
// (worst case ≈ 3.5k chars pretty-printed):
//   - data capped at 160 chars (meetingEnrich already slices to 160 — lossless
//     in practice; reach for getObjects(recordId) for the full text);
//   - score rounded to 4 decimals (raw float64 is ~19 chars of noise per hit);
//   - default limit 8 unless the caller asks for more (server default is 10).
var DATA_PREVIEW_CHARS = 160;
var DEFAULT_LIMIT = 8;

function _trimHits(res) {
  if (!res || !res.ok || !res.hits) return res;
  for (var i = 0; i < res.hits.length; i++) {
    var h = res.hits[i];
    if (!h) continue;
    if (typeof h.data === "string" && h.data.length > DATA_PREVIEW_CHARS) {
      h.data = h.data.slice(0, DATA_PREVIEW_CHARS) + "…";
    }
    if (typeof h.score === "number") {
      h.score = Math.round(h.score * 1e4) / 1e4;
    }
  }
  return res;
}

// createSemSearch(deps?) — factory, mainly for tests; inject a client.
export function createSemSearch(deps) {
  deps = deps || {};
  var client = deps.client || _newClient();

  // search(query, opts) → { ok, hits, mode, vectorStatus } (or { ok:false,
  // error, code }). opts: { space?, scopes?, limit?, mode?, require?, exclude? }.
  // Defaults to the server's hybrid mode (lexical + semantic fused, degrades to
  // fts when no embedder is reachable). The query string supports "phrases" and
  // prefix* on the lexical leg; require/exclude are must/must-not term arrays.
  // hits[].data is capped to a preview (see DATA_PREVIEW_CHARS) — a structured
  // object, ready to walk as `res.hits`, no JSON.stringify/logs.get round-trip.
  // limit defaults to DEFAULT_LIMIT (inline-render budget); pass an explicit
  // opts.limit for more.
  function search(query, opts) {
    opts = opts || {};
    if (opts.limit == null) {
      opts = Object.assign({}, opts, { limit: DEFAULT_LIMIT });
    }
    return _trimHits(client.search(query, opts));
  }

  return { search: search };
}

var _default = null;
function _getDefault() {
  if (!_default) _default = createSemSearch();
  return _default;
}

// Tool-facing method — lazily build the default instance so module import
// stays cheap (the boot prelude imports every tool).
export function search(query, opts) {
  return _getDefault().search(query, opts);
}
