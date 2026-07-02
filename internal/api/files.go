package api

// Files (files v2) — the per-space file surface wrapping the SDK's
// Space.Files(). Files always bind to an existing object; the payloads
// rows backing them live on a derived per-object child, so reads go
// through GET /files[...] (typed member view) or the per-object
// POST /objects/:objectId/files/query[/subscribe] (cleartext rows).
// The file BYTES ride plain HTTP — upload is a raw POST body, download
// a raw GET response with real Content-Type / Range support — the two
// deliberate non-JSON bodies in the API. See docs/16-files.md.

// FileInfo describes one attached file — the unsealed member view.
// Name / Mime / Variant / VariantOf come from the sealed (member-only)
// part of the payloads row and are empty for a keyless reader.
type FileInfo struct {
	FileId   string `json:"fileId"`
	ObjectId string `json:"objectId"`
	// RootCid is the content address of the encrypted file. Empty for
	// inline-tier files (bytes ride the CRDT row itself).
	RootCid string `json:"rootCid,omitempty"`
	// Size is the plaintext byte size.
	Size int64 `json:"size"`
	// Inline reports the inline tier (no rootCid, no backup needed).
	Inline bool `json:"inline"`
	// Durable reports a verified network-custody receipt (inline files
	// are durable by construction). False right after attach is normal:
	// backup runs in the background — watch /files/subscribe or re-GET.
	Durable bool `json:"durable"`
	// Cached reports a complete local copy (always true for inline).
	Cached    bool   `json:"cached"`
	Name      string `json:"name,omitempty"`
	Mime      string `json:"mime,omitempty"`
	Variant   string `json:"variant,omitempty"`
	VariantOf string `json:"variantOf,omitempty"`
}

// File durability states — FileStatus.State.
const (
	// FileStateDurable — verified network receipt recorded (or inline).
	FileStateDurable = "durable"
	// FileStateInFlight — registered, backup not confirmed yet.
	FileStateInFlight = "inflight"
	// FileStateLimited — the network refused backup (storage limit);
	// retried on a slow cadence and on POST .../retry.
	FileStateLimited = "limited"
)

// FileStatus is the point-in-time durability + availability view of
// one file. Also the payload of the per-space `status` SSE frames on
// GET /v1/spaces/:spaceId/files/subscribe.
type FileStatus struct {
	FileId   string `json:"fileId"`
	ObjectId string `json:"objectId"`
	State    string `json:"state"`
	Cached   bool   `json:"cached"`
	// Attempts counts failed background attempts since the last
	// success/enqueue; 0 when no work is pending.
	Attempts int    `json:"attempts,omitempty"`
	LastErr  string `json:"lastErr,omitempty"`
}

// FileStats are the space's aggregate durability counts
// (GET /v1/spaces/:spaceId/files/stats).
type FileStats struct {
	Total    int `json:"total"`
	Durable  int `json:"durable"`
	InFlight int `json:"inflight"`
	Limited  int `json:"limited"`
}

// FileListResponse is the body of GET /v1/spaces/:spaceId/files.
type FileListResponse struct {
	Files []FileInfo `json:"files"`
}

// FileCacheInfo is the body of GET /v1/files/cache — local bytes held
// by file content across all spaces (complete + partial copies).
type FileCacheInfo struct {
	Size int64 `json:"size"`
}

// FileCacheFreeRequest is the body of POST /v1/files/cache/free.
type FileCacheFreeRequest struct {
	Bytes int64 `json:"bytes"`
}

// FileCacheFreeResult reports the bytes actually reclaimed — less than
// requested when nothing else is safely evictable.
type FileCacheFreeResult struct {
	Freed int64 `json:"freed"`
}

// Error code namespace for file endpoints.
const (
	// ErrFileNotFound — unknown fileId, unknown objectId on attach, or
	// a files query against an object with no files attached yet.
	ErrFileNotFound = "file.not_found"
	// ErrFileNotDurable — offload refused: the local bytes are the only
	// copy (file not backed up yet). 409.
	ErrFileNotDurable = "file.not_durable"
	// ErrFileNotAvailable — content download refused: the bytes are not
	// local and cannot be fetched yet (file not durable, or the network
	// advertises no public read base). Retryable resource state — for a
	// freshly synced row, retry once the row shows networkSign (the
	// files/query/subscribe update event). 409.
	ErrFileNotAvailable = "file.not_available"
	// ErrFileVariantInvalid — variant/variantOf pairing broken, or the
	// variant original lives on a different object. 400.
	ErrFileVariantInvalid = "file.variant_invalid"
)
