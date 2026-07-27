package editor

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/anyproto/any-store/v2/anyenc"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/ensure"
)

// ErrNotFound signals that a referenced blockId does not exist on the
// object's editor_blocks dataset. Distinct from space.ErrNotFound so
// callers can map cleanly to 404 blocks.not_found.
var ErrNotFound = errors.New("blocks: block not found")

// Block is the wire / Go-side shape of one block, used by HTTP
// handlers, the markdown refactor, and callers.
type Block struct {
	Id    string         `json:"id"`
	Ver   map[string]any `json:"_ver,omitempty"`
	Type  string         `json:"type"`
	Style map[string]any `json:"style,omitempty"`
	Text  string         `json:"text,omitempty"`
	Nav   Nav            `json:"nav"`
}

// Nav is the per-record sibling-ordering namespace. Distinct from the
// `nav` virtual built-in used for the cross-space object tree (same
// shape, different scope).
type Nav struct {
	ParentId string `json:"parentId"`
	Pos      string `json:"pos"`
}

// CreateInput is the validated input to Create. Mirrors the wire
// body shape — fields default to safe values when omitted on the wire.
type CreateInput struct {
	Type     string
	Style    map[string]any // any-shaped; serialized as JSON object
	Text     string
	ParentId string
	Pos      string // empty → server allocates next-after-max
}

// PatchInput is the validated input to Patch. Each entry in Set is
// one dotted path → JSON value $set; each entry in Unset is one
// dotted path to $unset. Empty Set + empty Unset is a no-op.
type PatchInput struct {
	Set   map[string]json.RawMessage
	Unset []string
}

// List returns every block on the object's editor_blocks dataset in
// document order — depth-first, siblings sorted by nav.pos ascending.
// Meta fields (`_ver` etc.) are always present in query results, so
// callers receive `_ver` alongside payload fields (needed for the wire
// response and for client-side dedup).
//
// Empty result for objects with no body blocks yet (the dataset is
// empty until the first create).
func List(ctx context.Context, sp space.Space, objectId string) ([]Block, error) {
	docs, err := sp.Query(objectId, Dataset).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("blocks: List: query: %w", err)
	}

	blocks := make([]Block, 0, len(docs))
	for _, d := range docs {
		b, ok := recordToBlock(d)
		if !ok {
			continue
		}
		blocks = append(blocks, b)
	}
	return treeOrder(blocks), nil
}

// Get fetches one block by id and returns its wire shape (with _ver).
// Wraps space.ErrNotFound as ErrNotFound so callers can map to 404.
func Get(ctx context.Context, sp space.Space, objectId, blockId string) (Block, error) {
	doc, err := sp.Query(objectId, Dataset).
		Filter(map[string]any{"id": blockId}).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return Block{}, ErrNotFound
		}
		return Block{}, fmt.Errorf("blocks: Get: %w", err)
	}
	b, ok := recordToBlock(doc)
	if !ok {
		return Block{}, ErrNotFound
	}
	return b, nil
}

// Create issues one upsert with an empty record id so the SDK
// derives a stable id from the change CID (base58(xxh3-64(ChangeId))).
// Returns the raw space.ModifyResult — recordIds[0] is the derived
// block id, versionId correlates the write with the live event. Read
// the block back through /query with dataset=editor_blocks.
//
// If in.Pos is empty, the server reads the parent's current max pos
// and allocates the next lexid past it. Concurrent inserts may
// collide on the same pos — that's OK for sibling ordering; the
// lexid alphabet has enough headroom for clients to re-rank later.
// EnsureType attaches the editor type to the object's any.types so the
// membership-gated editor_blocks write is admitted by the SDK. Shared
// by Create and markdown.Set.
func EnsureType(ctx context.Context, sp space.Space, objectId string) error {
	return ensure.TypeAttached(ctx, sp, objectId, TypeId)
}

func Create(ctx context.Context, sp space.Space, objectId string, in CreateInput) (space.ModifyResult, error) {
	if in.Type == "" {
		return space.ModifyResult{}, fmt.Errorf("blocks: Create: type required")
	}
	if err := EnsureType(ctx, sp, objectId); err != nil {
		return space.ModifyResult{}, fmt.Errorf("blocks: Create: ensure type: %w", err)
	}
	if in.Pos == "" {
		maxPos, err := MaxPos(ctx, sp, objectId, in.ParentId)
		if err != nil {
			return space.ModifyResult{}, fmt.Errorf("blocks: Create: lookup max pos: %w", err)
		}
		in.Pos = NextPos(maxPos)
	}

	payload := map[string]any{
		FieldType: in.Type,
		FieldNav: map[string]any{
			NavParentId: in.ParentId,
			NavPos:      in.Pos,
		},
	}
	if in.Text != "" {
		payload[FieldText] = in.Text
	}
	if len(in.Style) > 0 {
		payload[FieldStyle] = in.Style
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id:     "",
			Upsert: true,
			Ops: []space.Op{{
				Type:  space.OpSet,
				Path:  "",
				Value: payload,
			}},
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("blocks: Create: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return space.ModifyResult{}, fmt.Errorf("blocks: Create: rejected: %s", res.Rejections[0].Reason)
	}
	if len(res.RecordIds) == 0 {
		return space.ModifyResult{}, fmt.Errorf("blocks: Create: empty RecordIds")
	}
	return res, nil
}

// Patch applies the set/unset paths atomically against blockId. The
// block must already exist (no upsert) — Patch on a missing record
// returns ErrNotFound. Empty patch is a no-op: no change is produced,
// so the result carries recordIds=[blockId] with an empty versionId.
//
// Each Set entry is one $set op against its dotted path; the path
// becomes the SDK's space.Op.Path and the JSON value (as
// json.RawMessage) gets unmarshalled into a Go-native value the SDK
// accepts. Unset entries become $unset ops, payload-less.
func Patch(ctx context.Context, sp space.Space, objectId, blockId string, in PatchInput) (space.ModifyResult, error) {
	if _, err := Get(ctx, sp, objectId, blockId); err != nil {
		return space.ModifyResult{}, err
	}

	ops := make([]space.Op, 0, len(in.Set)+len(in.Unset))
	for path, raw := range in.Set {
		val, err := decodeJSONValue(raw)
		if err != nil {
			return space.ModifyResult{}, fmt.Errorf("blocks: Patch: set %q: %w", path, err)
		}
		ops = append(ops, space.Op{
			Type:  space.OpSet,
			Path:  path,
			Value: val,
		})
	}
	for _, path := range in.Unset {
		ops = append(ops, space.Op{
			Type: space.OpUnset,
			Path: path,
		})
	}
	if len(ops) == 0 {
		return space.ModifyResult{RecordIds: []string{blockId}}, nil
	}

	res, err := sp.Modify(ctx, space.ModifyBatch{
		ObjectId: objectId,
		Dataset:  Dataset,
		Records: []space.RecordModify{{
			Id:  blockId,
			Ops: ops,
		}},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("blocks: Patch: modify: %w", err)
	}
	if len(res.Rejections) > 0 {
		return res, fmt.Errorf("blocks: Patch: rejected: %s", res.Rejections[0].Reason)
	}
	return res, nil
}

// Delete tombstones one block by id. Sticky — re-creating a block
// with the same id would be rejected by the SDK's tombstone rule.
// Children of the deleted block aren't cascaded automatically; the
// caller (or the markdown bulk path) is responsible for cleaning up.
func Delete(ctx context.Context, sp space.Space, objectId, blockId string) (space.ModifyResult, error) {
	res, err := sp.Delete(ctx, space.DeleteBatch{
		ObjectId:  objectId,
		Dataset:   Dataset,
		RecordIds: []string{blockId},
	})
	if err != nil {
		return space.ModifyResult{}, fmt.Errorf("blocks: Delete: %w", err)
	}
	return res, nil
}

// MaxPos returns the highest nav.pos string among blocks with the
// given parentId, or "" when the parent has no children yet. Used by
// Create to allocate the default tail position.
func MaxPos(ctx context.Context, sp space.Space, objectId, parentId string) (string, error) {
	doc, err := sp.Query(objectId, Dataset).
		Filter(map[string]any{"nav.parentId": parentId}).
		Sort("-nav.pos").
		Limit(1).
		One(ctx)
	if err != nil {
		if errors.Is(err, space.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("blocks: MaxPos parent=%q: %w", parentId, err)
	}
	if doc == nil {
		return "", nil
	}
	posVal := doc.Get(FieldNav, NavPos)
	if posVal == nil || posVal.Type() != anyenc.TypeString {
		return "", nil
	}
	return string(posVal.GetStringBytes()), nil
}

// --- helpers ---------------------------------------------------------------

// recordToBlock projects an anyenc record into the wire shape. Ok is
// false when the record is missing the mandatory id field (e.g.
// projection-only meta rows) — caller should skip.
func recordToBlock(rec *anyenc.Value) (Block, bool) {
	id := getString(rec, "id")
	if id == "" {
		return Block{}, false
	}
	b := Block{
		Id:   id,
		Type: getString(rec, FieldType),
		Text: getString(rec, FieldText),
		Nav: Nav{
			ParentId: getString(rec, FieldNav, NavParentId),
			Pos:      getString(rec, FieldNav, NavPos),
		},
	}
	if styleVal := rec.Get(FieldStyle); styleVal != nil && styleVal.Type() == anyenc.TypeObject {
		b.Style = anyencToMap(styleVal)
	}
	if verVal := rec.Get("_ver"); verVal != nil && verVal.Type() == anyenc.TypeObject {
		b.Ver = anyencToMap(verVal)
	}
	return b, true
}

func getString(v *anyenc.Value, path ...string) string {
	got := v.Get(path...)
	if got == nil || got.Type() != anyenc.TypeString {
		return ""
	}
	return string(got.GetStringBytes())
}

// anyencToMap converts an anyenc object value into a Go-native map.
// Used to surface `style` to JSON marshallers — the fastjson route
// other handlers take is overkill for this small per-block object,
// and `style` is already a map-shape on the wire.
func anyencToMap(v *anyenc.Value) map[string]any {
	if v == nil {
		return nil
	}
	out := map[string]any{}
	obj, _ := v.Object()
	if obj == nil {
		return out
	}
	obj.Visit(func(rawKey []byte, val *anyenc.Value) {
		out[string(rawKey)] = anyencValueToGo(val)
	})
	return out
}

func anyencValueToGo(v *anyenc.Value) any {
	if v == nil {
		return nil
	}
	switch v.Type() {
	case anyenc.TypeNull:
		return nil
	case anyenc.TypeTrue:
		return true
	case anyenc.TypeFalse:
		return false
	case anyenc.TypeNumber:
		// Most style values are ints (heading level). Fall back to
		// float when the value has a fractional part.
		if f := v.GetFloat64(); f == float64(int64(f)) {
			return int64(f)
		} else {
			return f
		}
	case anyenc.TypeString:
		return string(v.GetStringBytes())
	case anyenc.TypeArray:
		arr := v.GetArray()
		out := make([]any, 0, len(arr))
		for _, item := range arr {
			out = append(out, anyencValueToGo(item))
		}
		return out
	case anyenc.TypeObject:
		return anyencToMap(v)
	default:
		return nil
	}
}

// decodeJSONValue converts an incoming JSON value (as json.RawMessage)
// into a Go-native value the SDK accepts as space.Op.Value. The SDK
// accepts scalar / slice / map[string]any plus *fastjson.Value;
// json.Unmarshal into `any` gives us the first three shapes.
func decodeJSONValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("invalid JSON value: %w", err)
	}
	return v, nil
}

// treeOrder reorders a flat block slice into depth-first document
// order: top-level blocks first (sorted by pos), then each block's
// children inline after it. Stable — siblings sorted by pos
// ascending (lexids compare lexicographically).
//
// Orphan blocks (parentId points at a missing block) are appended
// at the end in pos order so the caller still sees them; in practice
// this only happens during transient sync gaps.
func treeOrder(blocks []Block) []Block {
	byParent := map[string][]Block{}
	known := map[string]struct{}{}
	for _, b := range blocks {
		byParent[b.Nav.ParentId] = append(byParent[b.Nav.ParentId], b)
		known[b.Id] = struct{}{}
	}
	for parent := range byParent {
		children := byParent[parent]
		slices.SortStableFunc(children, func(a, b Block) int {
			return cmp.Compare(a.Nav.Pos, b.Nav.Pos)
		})
		byParent[parent] = children
	}

	out := make([]Block, 0, len(blocks))
	var walk func(parent string)
	walk = func(parent string) {
		for _, b := range byParent[parent] {
			out = append(out, b)
			walk(b.Id)
		}
	}
	walk(RootParentId)

	// Surface orphans (parent not in `known`) in a single pass so
	// callers don't lose them on transient sync gaps.
	for _, b := range blocks {
		if b.Nav.ParentId == RootParentId {
			continue
		}
		if _, ok := known[b.Nav.ParentId]; ok {
			continue
		}
		out = append(out, b)
	}
	return out
}
