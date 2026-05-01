package markdown

import (
	"context"

	"github.com/anyproto/any-sync-sdk/handler"
)

// TypeId is the type identifier callers register `markdown` under.
// Used as the second argument to space.Objects().Create when minting
// a new markdown object.
const TypeId = "md"

// Dataset is the per-object dataset that holds the block records.
// Each record is one markdown block; the record id is the lexid
// that determines the block's position in the document.
const Dataset = "md_blocks"

// FieldText is the only payload field on a block record. Always a
// string; Set writes raw markdown for the block (heading hashes,
// fence markers, list bullets, blockquote `>` prefixes are all
// retained verbatim). Round-tripping rebuilds the document by
// joining FieldText values in lexid order with "\n\n".
const FieldText = "text"

// dataVersion is the on-the-wire stamp pinned to writes on this
// dataset. Bump the suffix only when validation logic changes in a
// way that must reject older writers.
const dataVersion = "md_blocks-v1"

// NewType returns the handler.Type to add to config.Config.Types so
// the SDK accepts writes on the markdown blocks dataset.
//
//	cfg := config.Config{
//	    Types: []handler.Type{ markdown.NewType() },
//	    ...
//	}
func NewType() handler.Type {
	return handler.Type{
		Id:          TypeId,
		Name:        "Markdown",
		Description: "Plain markdown document, stored as a stream of block-records",
		Handlers: []handler.Registration{{
			Handler:     blocksHandler{},
			DataVersion: dataVersion,
		}},
	}
}

// blocksHandler is the CRDT handler owning the `md_blocks` dataset.
// Validation is intentionally minimal: any Set on the `text` field
// is accepted. Per-block diff/split logic lives in api.go (caller
// side); the handler is the thin write gate.
type blocksHandler struct {
	handler.DefaultHandler
}

func (blocksHandler) Dataset() string              { return Dataset }
func (blocksHandler) Version() int                 { return 1 }
func (blocksHandler) Init(_ context.Context) error { return nil }

func (blocksHandler) BeforeCreate(_ *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	return nil
}

func (blocksHandler) BeforeModify(_ *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Op, _ *handler.Sink) error {
	return nil
}

func (blocksHandler) BeforeDelete(_ *handler.ChangeCtx, _ *handler.RecordChange, _ *handler.Sink) error {
	return nil
}
