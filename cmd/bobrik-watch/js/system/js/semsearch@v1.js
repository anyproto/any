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
// short indexed text, not full records — hydrate via getObjects"). We cap it so
// a default-limit result renders inline in the run_cell kernel instead of
// tripping the >4000-char value-store stub — the stub forces a logs.get
// round-trip that bites the model when it has logged a stringified copy. Both
// callers already slice data well under this (meetingEnrich takes 160), so the
// cap is lossless in practice; reach for getObjects(recordId) for the full text.
var DATA_PREVIEW_CHARS = 200;

function _trimHits(res) {
  if (!res || !res.ok || !res.hits) return res;
  for (var i = 0; i < res.hits.length; i++) {
    var h = res.hits[i];
    if (h && typeof h.data === "string" && h.data.length > DATA_PREVIEW_CHARS) {
      h.data = h.data.slice(0, DATA_PREVIEW_CHARS) + "…";
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
  function search(query, opts) {
    return _trimHits(client.search(query, opts || {}));
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
