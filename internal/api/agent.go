package api

// Agent data layer — request/response types for the agent_turns /
// agent_chunks datasets (internal/agentlog, attached to the chat
// object) and the agent_memory_items dataset (internal/agentmem,
// attached to the per-space brain object). All writes return the
// shared api.ModifyResult; reads go through POST /query (or
// /query/subscribe) with the matching dataset name. See
// docs/11-agent-memory.md for the layering model.

// LLMStats is the per-turn LLM summary embedded in a turn record.
// Cheap scalars duplicated from the run trace for convenience — the
// heavy per-LLM-API-turn detail (cells, raw responses) lives in the
// run trace object, reachable via the turn's traceRef.
// StopReason is one of the neutral invocation outcomes: done | wrapup |
// break_soft | break_hard | length | error (agentlog.StopReasons).
type LLMStats struct {
	StopReason string  `json:"stopReason,omitempty"`
	InTokens   int     `json:"inTokens,omitempty"`
	OutTokens  int     `json:"outTokens,omitempty"`
	CacheRead  int     `json:"cacheRead,omitempty"`
	CacheWrite int     `json:"cacheWrite,omitempty"`
	Model      string  `json:"model,omitempty"`
	CostUsd    float64 `json:"costUsd,omitempty"`
	FuelUsed   int     `json:"fuelUsed,omitempty"`
	Cells      int     `json:"cells,omitempty"`
}

// AgentTurnAppendRequest is the body of
// POST /v1/spaces/:spaceId/objects/:objectId/agent/turns.
//
// One record per agent invocation (human message → end_turn),
// write-once (never edited; author-only delete for history wipes).
// `seq` is the per-chat monotonic ordering
// key; OMIT it and the server allocates max+1 (the normal path,
// retrying on collision — no client probe/retry). Provide it only to
// control the seq explicitly (a duplicate then surfaces as an error).
// The record id is derived from seq (zero-padded) so lexical id order
// matches insertion order. Server stamps creator / createdAt.
type AgentTurnAppendRequest struct {
	Seq         *int      `json:"seq,omitempty"`
	FromAgent   string    `json:"fromAgent,omitempty"`
	UserName    string    `json:"userName,omitempty"`
	UserText    string    `json:"userText,omitempty"`
	Think       string    `json:"think,omitempty"`
	Replies     []string  `json:"replies,omitempty"`
	Effects     []string  `json:"effects,omitempty"`
	MessageIds  []string  `json:"messageIds,omitempty"`
	TraceRef    string    `json:"traceRef,omitempty"`
	Interrupted bool      `json:"interrupted,omitempty"`
	LLM         *LLMStats `json:"llm,omitempty"`
}

// AgentChunkCreateRequest is the body of
// POST /v1/spaces/:spaceId/objects/:objectId/agent/chunks.
//
// One record per compression event, immutable. `fromSeq`/`toSeq` are
// the EXPLICIT inclusive pointers into the same object's agent_turns
// dataset — the raw range this summary covers; drill-down is
// `{seq: {$gte: fromSeq, $lte: toSeq}}`. periodStart/periodEnd are
// unix seconds so period range filters stay indexable.
// `level` is the hierarchical-compression tier (ADR-006 §2): 1
// summarizes agent_turns, 2+ summarizes level-(N-1) chunks. Omit for
// the common level-1 case (server defaults to 1). fromSeq/toSeq point
// at the CHILD seqs at the level below.
type AgentChunkCreateRequest struct {
	Seq          *int   `json:"seq,omitempty"`
	Level        *int   `json:"level,omitempty"`
	FromAgent    string `json:"fromAgent,omitempty"`
	Summary      string `json:"summary"`
	PeriodStart  int64  `json:"periodStart"`
	PeriodEnd    int64  `json:"periodEnd"`
	FromSeq      *int   `json:"fromSeq"`
	ToSeq        *int   `json:"toSeq"`
	TurnsCovered int    `json:"turnsCovered,omitempty"`
}

// Edge is one typed link between memory items. `strength` is 0..1.
type Edge struct {
	To       string  `json:"to"`
	Type     string  `json:"type"`
	Strength float64 `json:"strength,omitempty"`
}

// AgentMemoryCreateRequest is the body of
// POST /v1/spaces/:spaceId/agent/memory. The server resolves the
// per-space brain object (see AgentBrainResponse) and writes the item
// to its agent_memory_items dataset.
//
// `category` is a required, non-empty lowercase slug — an OPEN set
// (documented builtins: claim, preference, decision, lesson, episode,
// taskstate, insight; agents may invent new ones). `context` is the
// required one-line summary. Numeric fields absent in the request get
// server defaults: importance 5, confidence 5, salience 10,
// accessCount 0, validFrom = createdAt.
//
// There is deliberately no vector field — semantic indexing is
// external (it tails /query/subscribe); embeddingRef is reserved for
// a future external-index backref and not settable in v1.
type AgentMemoryCreateRequest struct {
	FromAgent  string   `json:"fromAgent,omitempty"`
	Category   string   `json:"category"`
	Context    string   `json:"context"`
	Body       string   `json:"body,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Entities   []string `json:"entities,omitempty"`
	Keywords   []string `json:"keywords,omitempty"`
	Confidence *int     `json:"confidence,omitempty"`
	Importance *int     `json:"importance,omitempty"`
	Salience   *float64 `json:"salience,omitempty"`
	ValidFrom  int64    `json:"validFrom,omitempty"`
	Edges      []Edge   `json:"edges,omitempty"`
	ChatId     string   `json:"chatId,omitempty"`
}

// AgentMemoryEvolveRequest is the body of
// PATCH /v1/spaces/:spaceId/agent/memory/:itemId. Pointer/nil-able
// fields distinguish "leave as is" (absent) from "set" — matching the
// handler's mutable allow-list. Author-only; modifiedAt is bumped
// server-side. At least one field must be present
// (400 request.missing_field otherwise).
type AgentMemoryEvolveRequest struct {
	Salience    *float64  `json:"salience,omitempty"`
	AccessCount *int      `json:"accessCount,omitempty"`
	Confidence  *int      `json:"confidence,omitempty"`
	Importance  *int      `json:"importance,omitempty"`
	Context     *string   `json:"context,omitempty"`
	Body        *string   `json:"body,omitempty"`
	Tags        *[]string `json:"tags,omitempty"`
	Edges       *[]Edge   `json:"edges,omitempty"`
}

// AgentBrainResponse is the body of GET /v1/spaces/:spaceId/agent/brain
// — the deterministic per-space object id that hosts the
// agent_memory_items dataset. Derived from a fixed seed (the same
// objects/derive primitive the UI uses for the primary chat), so every
// caller computes the same id with no discovery query and no
// create race.
type AgentBrainResponse struct {
	ObjectId string `json:"objectId"`
}

// Error code namespace for the agent data layer.
const (
	ErrAgentSeqRequired    = "agent.seq_required"
	ErrAgentTurnInvalid    = "agent.turn_invalid"
	ErrAgentChunkInvalid   = "agent.chunk_invalid"
	ErrAgentMemoryInvalid  = "agent.memory_invalid"
	ErrAgentMemoryNotFound = "agent.memory_not_found"
	ErrAgentNotAuthor      = "agent.not_author"
	ErrAgentSeqDeleted     = "agent.seq_deleted"
)
