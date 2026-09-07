package api

// Links — edges the server's link index holds (docs/13-index.md
// § Links). One edge is a source place (an object plus the record or
// property value the reference was found in), a kind, and the
// canonical target.

// LinkSource is where an edge was found: the object, the collection
// (a module or runtime collection, or the virtual `prop` for a
// property value) and the record (the property id under `prop`).
type LinkSource struct {
	SpaceId  string `json:"spaceId"`
	ObjectId string `json:"objectId"`
	Dataset  string `json:"dataset"`
	RecordId string `json:"recordId"`
}

// LinkTarget is the canonical any:// target, parsed. Uri is the
// canonical string (docs/19-links.md); Kind is the URI kind (o, p, m,
// f); the id fields are populated per kind.
type LinkTarget struct {
	Uri      string `json:"uri"`
	Kind     string `json:"kind"`
	SpaceId  string `json:"spaceId"`
	ObjectId string `json:"objectId,omitempty"`
	Dataset  string `json:"dataset,omitempty"`
	RecordId string `json:"recordId,omitempty"`
	PropId   string `json:"propId,omitempty"`
	Identity string `json:"identity,omitempty"`
	FileId   string `json:"fileId,omitempty"`
}

// Link is one edge. Kind is the edge kind: mention, link, card, embed,
// relation (an open set — see docs/13-index.md § Links).
type Link struct {
	Source LinkSource `json:"source"`
	Kind   string     `json:"kind"`
	Target LinkTarget `json:"target"`
}

// Link kinds — the established vocabulary.
const (
	LinkKindMention  = "mention"
	LinkKindLink     = "link"
	LinkKindCard     = "card"
	LinkKindEmbed    = "embed"
	LinkKindRelation = "relation"
)

// BacklinksResponse is the body of
// GET /v1/spaces/:spaceId/objects/:objectId/backlinks. Object holds
// the edges pointing at the object itself; Parts the edges pointing
// at one of its records or property values (a block link stays a
// block link — the parent is not counted twice). When the read is
// narrowed to one part (`?record=` / `?prop=`), Object holds the
// edges to that part and Parts is empty. Neither is ever null.
type BacklinksResponse struct {
	Object []Link `json:"object"`
	Parts  []Link `json:"parts"`
}

// LinksResponse is the body of
// GET /v1/spaces/:spaceId/objects/:objectId/links — the edges whose
// source is the object (or the one record / property it was narrowed
// to). Never null.
type LinksResponse struct {
	Links []Link `json:"links"`
}

// SpaceBacklinks is one space's share of an account-wide read.
type SpaceBacklinks struct {
	SpaceId string `json:"spaceId"`
	Object  []Link `json:"object"`
	Parts   []Link `json:"parts"`
}

// BacklinksAllResponse is the body of GET /v1/backlinks?target=… —
// the edges pointing at one target from every space this device
// indexes. Spaces without an edge are omitted; never null.
type BacklinksAllResponse struct {
	Spaces []SpaceBacklinks `json:"spaces"`
}

// EventLinksUpdated is the device-scope event the link index
// publishes after a page changed edges: data is EventLinksUpdatedData.
const EventLinksUpdated = "links.updated"

// EventLinksUpdatedData is the payload of EventLinksUpdated: the
// canonical targets (object references, or the identity / file URI)
// whose backlinks changed in spaceId.
type EventLinksUpdatedData struct {
	SpaceId string   `json:"spaceId"`
	Targets []string `json:"targets"`
}
