package api

// Search modes. Hybrid runs both legs and fuses by reciprocal rank;
// fts / vector run one leg only.
const (
	SearchModeHybrid = "hybrid"
	SearchModeFTS    = "fts"
	SearchModeVector = "vector"
)

// SearchRequest is the body of POST /v1/spaces/:spaceId/search. The
// search runs over the server's local index (see docs/13-index.md).
type SearchRequest struct {
	// Query is the search text. Required.
	Query string `json:"query"`
	// Scopes restricts results to the given index scopes (basic, chat,
	// props, …). Empty = all scopes.
	Scopes []string `json:"scopes,omitempty"`
	// Limit caps returned records — every hit is a distinct
	// (objectId, dataset, recordId). Default 10, max 100.
	Limit int `json:"limit,omitempty"`
	// Mode is hybrid (default), fts, or vector. Vector requires an
	// embedder configured on the server.
	Mode string `json:"mode,omitempty"`
	// Require / Exclude are extra must / must-not terms ($require /
	// $exclude) — a hit must contain every Require term and no Exclude
	// term, in every mode: the FTS leg matches on them, and vector hits
	// are post-filtered against the FTS index before fusion. Each term
	// may be a "phrase" or prefix*.
	Require []string `json:"require,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
	// MaxData bounds each hit's Data to a window of at most this many
	// runes around the first query/require term match (the head when
	// nothing matches). 0 = DefaultSearchMaxData; -1 = the whole indexed
	// chunk text. DataOffset / DataTotal on the hit locate the window.
	MaxData int `json:"maxData,omitempty"`
	// Passages asks for up to this many further matching chunks per
	// record, best first, on hit.passages (0 = none, max 10). They are
	// the record's other chunks that ranked within the search window,
	// not every chunk of the record.
	Passages int `json:"passages,omitempty"`
}

// MaxSearchPassages caps SearchRequest.Passages.
const MaxSearchPassages = 10

// DefaultSearchMaxData is the Data window applied when a request leaves
// MaxData unset: enough for a one-line preview or an agent to judge the
// match, and a bounded reply whatever the record size — the full record
// stays one dataset query away.
const DefaultSearchMaxData = 512

// SearchHit is one ranked result — the indexed record's identity plus
// its indexed text. Score semantics depend on the effective mode: BM25
// for fts, cosine similarity for vector, RRF for hybrid; within one
// response higher is always better.
type SearchHit struct {
	Scope    string `json:"scope"`
	ObjectId string `json:"objectId"`
	Dataset  string `json:"dataset"`
	RecordId string `json:"recordId"`
	// Chunk is the 0-based chunk of the record this hit shows: long
	// records are indexed as several docs, and the hit is the record's
	// best-ranked one. One hit per record — no client-side dedupe.
	Chunk int `json:"chunk,omitempty"`
	// Data is the hit's indexed text, windowed to MaxData runes around
	// the first matching term. DataOffset is the window's rune offset
	// into the chunk's full indexed text and DataTotal that text's rune
	// length — Data is the whole text iff DataOffset == 0 and
	// len([]rune(Data)) == DataTotal.
	Data       string  `json:"data"`
	DataOffset int     `json:"dataOffset,omitempty"`
	DataTotal  int     `json:"dataTotal"`
	Score      float64 `json:"score"`
	// Passages are the record's next best matching chunks. Absent when
	// the request did not ask (SearchRequest.Passages) and when the
	// record has no other matching chunk in the search window.
	Passages []SearchPassage `json:"passages,omitempty"`
}

// SearchPassage is one further matching chunk of a hit's record, with
// the same Data window fields as the hit.
type SearchPassage struct {
	Chunk      int     `json:"chunk,omitempty"`
	Data       string  `json:"data"`
	DataOffset int     `json:"dataOffset,omitempty"`
	DataTotal  int     `json:"dataTotal"`
	Score      float64 `json:"score"`
}

// VectorStatus values — the search response tells the consumer (often
// an agent deciding how much to trust recall) what happened to the
// vector leg, not just that it silently fell back to lexical search.
const (
	// VectorStatusUsed: the vector leg ran and contributed to ranking.
	VectorStatusUsed = "used"
	// VectorStatusUnavailable: an embedder is configured but did not
	// answer for this query — unreachable, or not within the query
	// budget — retry later may differ. Hybrid degraded to FTS;
	// mode=vector would have returned 503.
	VectorStatusUnavailable = "unavailable"
	// VectorStatusDisabled: no embedder is configured on this server —
	// vector search can never run until config changes.
	VectorStatusDisabled = "disabled"
	// VectorStatusSkipped: the caller asked for mode=fts, vector was
	// not attempted (but is available on this server).
	VectorStatusSkipped = "skipped"
)

// SearchResponse is the reply. Mode reports the mode that actually ran:
// a hybrid request degrades to "fts" when no embedder is configured or
// the query embedding failed — VectorStatus says which of those it was.
type SearchResponse struct {
	Hits []SearchHit `json:"hits"`
	Mode string      `json:"mode"`
	// VectorStatus: used | unavailable | disabled | skipped — whether
	// semantic recall participated in this response and, if not, why.
	VectorStatus string `json:"vectorStatus"`
}
