package api

// EnrichedDataCreateRequest is the body for
// POST /v1/spaces/:spaceId/objects/:objectId/enriched-data — one sourced
// enrichment record written onto the target object's enriched_data collection.
//
// text is required. source is the provenance link list — comma-joined
// dataset-record URIs (any://o/<space>/<transcript>/editor_blocks/<blockId>,…),
// or the single object URI for a block-less source; stored pre-WEB-42 records
// carry the legacy fragment form (any://<space>/<transcript>#<blockId>,…),
// which readers keep parsing. target/value are set only for
// PROPERTY enrichments: target is the "<typeXKey>.<propXKey>" path the apply
// step also set on the object, value the value it set — so the UI can show the
// property's value came from this source. Collection enrichments leave both "".
type EnrichedDataCreateRequest struct {
	Text   string `json:"text"`
	Source string `json:"source"`
	Target string `json:"target"`
	Value  string `json:"value"`
}

// Error code namespace for enriched-data endpoints.
const (
	ErrEnrichedDataTextTooLong = "enricheddata.text_too_long"
)
