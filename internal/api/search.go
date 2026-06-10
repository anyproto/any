package api

// Search modes. Hybrid runs both legs and fuses by reciprocal rank;
// fts / vector run one leg only.
const (
	SearchModeHybrid = "hybrid"
	SearchModeFTS    = "fts"
	SearchModeVector = "vector"
)

// SearchRequest is the body of POST /v1/spaces/:spaceId/search. The
// search runs over the server's local index (see docs/11-index.md) —
// only content written after indexing started is found.
type SearchRequest struct {
	// Query is the search text. Required.
	Query string `json:"query"`
	// Scopes restricts results to the given index scopes (basic, chat,
	// agent). Empty = all scopes.
	Scopes []string `json:"scopes,omitempty"`
	// Limit caps returned hits. Default 10, max 100.
	Limit int `json:"limit,omitempty"`
	// Mode is hybrid (default), fts, or vector. Vector requires an
	// embedder configured on the server.
	Mode string `json:"mode,omitempty"`
}

// SearchHit is one ranked result — the indexed record's identity plus
// its indexed text. Score semantics depend on the effective mode: BM25
// for fts, cosine similarity for vector, RRF for hybrid; within one
// response higher is always better.
type SearchHit struct {
	Scope    string  `json:"scope"`
	ObjectId string  `json:"objectId"`
	Dataset  string  `json:"dataset"`
	RecordId string  `json:"recordId"`
	Data     string  `json:"data"`
	Score    float64 `json:"score"`
}

// SearchResponse is the reply. Mode reports the mode that actually ran:
// a hybrid request degrades to "fts" when no embedder is configured or
// the query embedding failed.
type SearchResponse struct {
	Hits []SearchHit `json:"hits"`
	Mode string      `json:"mode"`
}
