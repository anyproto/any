package api

// Doc-only request/response structs for handlers that parse with
// fastjson or use anonymous inline structs. These exist solely so
// swaggo can generate accurate OpenAPI schemas; they are never
// instantiated at runtime.

// ObjectCreateRequest documents the body of POST /v1/spaces/:spaceId/objects.
type ObjectCreateRequest struct {
	Types             []string                  `json:"types,omitempty"`
	InitialProperties map[string]map[string]any `json:"initialProperties,omitempty"`
	Nav               *ObjectCreateNav          `json:"nav,omitempty"`
}

// ObjectCreateNav documents the optional nav override in ObjectCreateRequest.
type ObjectCreateNav struct {
	Type     int    `json:"type,omitempty" enums:"1,2"`
	ParentId string `json:"parentId,omitempty"`
	Pos      string `json:"pos,omitempty"`
}

// SpaceQueryObjectsRequest documents the body of POST /v1/spaces/:spaceId/objects/query.
type SpaceQueryObjectsRequest struct {
	Filter map[string]any `json:"filter,omitempty"`
	Sort   []string       `json:"sort,omitempty"`
	Limit  int            `json:"limit,omitempty"`
	Offset int            `json:"offset,omitempty"`
}

// SpaceQueryRequest documents the body of POST /v1/spaces/:spaceId/query.
type SpaceQueryRequest struct {
	ObjectId string         `json:"objectId"`
	Dataset  string         `json:"dataset"`
	Filter   map[string]any `json:"filter,omitempty"`
	Sort     []string       `json:"sort,omitempty"`
	Limit    int            `json:"limit,omitempty"`
	Offset   int            `json:"offset,omitempty"`
}

// SpaceAggregateObjectsRequest documents the body of
// POST /v1/spaces/:spaceId/objects/aggregate.
type SpaceAggregateObjectsRequest struct {
	Pipeline         []map[string]any `json:"pipeline"`
	GroupLimit       *int             `json:"groupLimit,omitempty"`
	AccumArrayLimit  *int             `json:"accumArrayLimit,omitempty"`
	MemoryLimitBytes *int             `json:"memoryLimitBytes,omitempty"`
	Explain          bool             `json:"explain,omitempty"`
}

// SpaceAggregateRequest documents the body of
// POST /v1/spaces/:spaceId/aggregate.
type SpaceAggregateRequest struct {
	ObjectId         string           `json:"objectId"`
	Dataset          string           `json:"dataset"`
	Pipeline         []map[string]any `json:"pipeline"`
	GroupLimit       *int             `json:"groupLimit,omitempty"`
	AccumArrayLimit  *int             `json:"accumArrayLimit,omitempty"`
	MemoryLimitBytes *int             `json:"memoryLimitBytes,omitempty"`
	Explain          bool             `json:"explain,omitempty"`
}

// SpaceModifyRequest documents the body of POST /v1/spaces/:spaceId/modify.
type SpaceModifyRequest struct {
	ObjectId string         `json:"objectId"`
	Dataset  string         `json:"dataset"`
	Records  []RecordModify `json:"records"`
	TraceIds []string       `json:"traceIds,omitempty"`
}

// RecordModify documents one record in a modify batch.
type RecordModify struct {
	Id     string `json:"id,omitempty"`
	Upsert bool   `json:"upsert,omitempty"`
	Ops    []Op   `json:"ops"`
}

// Op documents one operation in a modify batch.
type Op struct {
	Type  string `json:"type" enums:"$set,$unset,$inc,$addToSet,$pull"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// DeleteRecordsRequest documents the body of POST /v1/spaces/:spaceId/delete-records.
type DeleteRecordsRequest struct {
	ObjectId  string   `json:"objectId"`
	Dataset   string   `json:"dataset,omitempty"`
	RecordIds []string `json:"recordIds"`
	TraceIds  []string `json:"traceIds,omitempty"`
}

// PropertiesSetBaseRequest documents the body of POST /v1/spaces/:spaceId/properties/:objectId/base/:typeId.
type PropertiesSetBaseRequest struct {
	Patch map[string]any `json:"patch"`
}

// MarkdownContent documents the body of GET/PUT .../editor/markdown.
type MarkdownContent struct {
	Content string `json:"content"`
}

// MarkdownSetResponse documents the response of PUT .../editor/markdown.
type MarkdownSetResponse struct {
	Inserted  []string `json:"inserted"`
	Updated   []string `json:"updated"`
	Deleted   []string `json:"deleted"`
	Unchanged int      `json:"unchanged"`
}
