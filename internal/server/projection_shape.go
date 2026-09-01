package server

import (
	"bytes"

	"github.com/valyala/fastjson"

	"github.com/anyproto/any-store/v2/anyenc"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// recordShaper renders stored records and change ops onto the wire.
// Two stages, in this order: the caller's projection, then the
// server's own blocklist. The blocklist runs last and unconditionally
// — naming a withheld field in a projection must not surface it.
//
// The zero value is the historical behaviour: whole records, nothing
// withheld.
type recordShaper struct {
	proj *projection
	// strip lists top-level fields the server withholds regardless of
	// what the caller asked for (key material on tech-space rows — see
	// spaceListStrippedFields).
	strip []string
}

// record renders one stored record.
func (s recordShaper) record(doc *anyenc.Value, a *fastjson.Arena) *fastjson.Value {
	if doc == nil {
		return nil
	}
	var out *fastjson.Value
	if s.proj != nil {
		out = s.proj.record(doc, a)
	} else {
		out = doc.FastJson(a)
	}
	for _, key := range s.strip {
		out.Del(key)
	}
	return out
}

// stripped reports whether an op path lands on a withheld field.
func (s recordShaper) stripped(path []string) bool {
	if len(path) == 0 {
		return false
	}
	for _, key := range s.strip {
		if key == path[0] {
			return true
		}
	}
	return false
}

// op renders one change op, or reports ok=false to drop it.
//
// Only $set and $unset reach the wire — the SDK normalises $inc /
// $addToSet / $pull into a $set at the op's path (its
// internal/subscribe projectOp) — so there are exactly three cases:
// the op is inside the projection (ship it), it straddles the boundary
// (narrow its payload), or it is outside (drop it). A $unset that
// straddles stays whole: the client drops the subtree it holds, which
// is the same end state.
func (s recordShaper) op(op space.EventOp, a *fastjson.Arena) []api.SubscribeEventOp {
	if s.proj == nil && len(s.strip) == 0 {
		// Nothing to shape: hand the op through untouched, so a request
		// without a projection is byte-identical to what it always was.
		return []api.SubscribeEventOp{rawOp(op, a)}
	}
	if s.stripped(op.Path) {
		return nil
	}
	if len(op.Path) == 0 && op.Payload != nil && op.Payload.Type() == anyenc.TypeObject {
		return s.multiFieldOp(op, a)
	}
	var node *projNode
	if s.proj != nil {
		verdict, n := s.proj.opVerdict(op.Path)
		if verdict == projOpDrop {
			return nil
		}
		node = n
	}
	out := api.SubscribeEventOp{Type: string(op.Type), Path: opPath(op)}
	if op.Payload != nil {
		shaped := shapePayload(op.Payload, node, a)
		if shaped == nil {
			// The new value holds none of the projected descendants, so
			// for the client the field is now gone. Say that — a $set of
			// an empty object would leave the mirror holding `{}` where
			// the doc in the same frame omits the field.
			out.Type = string(space.OpUnset)
			return []api.SubscribeEventOp{out}
		}
		out.Payload = shaped.MarshalTo(nil)
	}
	return []api.SubscribeEventOp{out}
}

// rawOp renders an op with no shaping applied.
func rawOp(op space.EventOp, a *fastjson.Arena) api.SubscribeEventOp {
	out := api.SubscribeEventOp{Type: string(op.Type), Path: opPath(op)}
	if op.Payload != nil {
		out.Payload = op.Payload.FastJson(a).MarshalTo(nil)
	}
	return out
}

// opPath renders an op's path, never nil — the wire shape has always
// carried `[]` for the multi-field form.
func opPath(op space.EventOp) []string {
	if op.Path == nil {
		return []string{}
	}
	return op.Path
}

// shapePayload renders a single-path op's payload against the
// projection node the path walk landed on. A nil node means the walk
// fell off inside an included subtree — nothing below is shaped, so
// the payload ships verbatim. Otherwise it goes through the same
// deepest-mark-wins rule the record body uses, which is what keeps an
// op's payload exactly as wide as the doc the client holds.
// nil means the new value holds nothing the client can see; the caller
// turns that into an $unset so the op and the doc in the same frame
// agree that the field is gone.
func shapePayload(v *anyenc.Value, node *projNode, a *fastjson.Arena) *fastjson.Value {
	if node == nil {
		return v.FastJson(a)
	}
	return projectValue(v, node, a)
}

// multiFieldOp shapes the multi-field form ($set / $unset with an
// empty path, payload an object whose KEYS ARE DOTTED PATHS applied in
// parallel — the record-creation shape). Each key is classified on its
// own and the op is dropped when nothing survives, since the form is a
// parallel merge and an empty one says nothing.
//
// A $unset's payload VALUES are placeholders the CRDT ignores, so only
// its keys are classified — narrowing a placeholder would be
// meaningless and would drop the removal.
//
// A $set key whose new value narrows to nothing means the field is
// gone for this client, which no $set can express. Those keys move to
// a second op, a multi-field $unset, so the ops and the doc in the
// same frame agree.
func (s recordShaper) multiFieldOp(op space.EventOp, a *fastjson.Arena) []api.SubscribeEventOp {
	obj, err := op.Payload.Object()
	if err != nil {
		return nil
	}
	unsetForm := space.OpType(op.Type) == space.OpUnset

	kept := a.NewObject()
	var vanished *fastjson.Value // $set keys that narrowed away
	keptAny, vanishedAny := false, false

	obj.Visit(func(key []byte, v *anyenc.Value) {
		verdict, node, ok := s.multiFieldKey(key)
		if !ok || verdict == projOpDrop {
			return
		}
		if unsetForm {
			kept.Set(string(key), v.FastJson(a))
			keptAny = true
			return
		}
		shaped := shapePayload(v, node, a)
		if shaped == nil {
			if vanished == nil {
				vanished = a.NewObject()
			}
			vanished.Set(string(key), a.NewTrue())
			vanishedAny = true
			return
		}
		kept.Set(string(key), shaped)
		keptAny = true
	})

	var out []api.SubscribeEventOp
	if keptAny {
		out = append(out, api.SubscribeEventOp{
			Type:    string(op.Type),
			Path:    []string{},
			Payload: kept.MarshalTo(nil),
		})
	}
	if vanishedAny {
		out = append(out, api.SubscribeEventOp{
			Type:    string(space.OpUnset),
			Path:    []string{},
			Payload: vanished.MarshalTo(nil),
		})
	}
	return out
}

// multiFieldKey classifies one dotted key of a multi-field payload.
// ok=false means the key is withheld by the server blocklist: the key
// is a PATH, so "guestKey.x" is withheld along with "guestKey".
func (s recordShaper) multiFieldKey(key []byte) (int, *projNode, bool) {
	if len(s.strip) > 0 {
		top := key
		if i := bytes.IndexByte(top, '.'); i >= 0 {
			top = top[:i]
		}
		for _, k := range s.strip {
			if string(top) == k {
				return projOpDrop, nil, false
			}
		}
	}
	if s.proj == nil {
		return projOpKeep, nil, true
	}
	verdict, node := s.proj.opVerdictBytes(key)
	return verdict, node, true
}

// opVerdictBytes is opVerdict over a dotted []byte key, so a
// multi-field payload is classified without splitting it into a
// []string first.
func (p *projection) opVerdictBytes(key []byte) (int, *projNode) {
	node := &p.root
	inherited := !p.userInclude
	for rest := key; len(rest) > 0; {
		seg := rest
		if i := bytes.IndexByte(rest, '.'); i >= 0 {
			seg, rest = rest[:i], rest[i+1:]
		} else {
			rest = nil
		}
		c := node.findBytes(seg)
		if c == nil {
			if inherited {
				return projOpKeep, nil
			}
			return projOpDrop, nil
		}
		switch c.mark {
		case projMarkExclude:
			if !c.incBelow {
				return projOpDrop, nil
			}
			inherited = false
		case projMarkInclude:
			inherited = true
		}
		node = c
	}
	if inherited {
		return projOpKeep, node
	}
	if node.incBelow {
		return projOpNarrow, node
	}
	return projOpDrop, nil
}

// findBytes is find over a []byte key. `string(b) == s` compiles to a
// comparison, not an allocation.
func (n *projNode) findBytes(key []byte) *projNode {
	for i := range n.children {
		if string(key) == n.children[i].key {
			return &n.children[i]
		}
	}
	return nil
}
