package server

import (
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-store/v2/anyenc"
)

// Projection shapes stored records onto the wire. Grammar is mongo's:
// a flat object of dotted paths to 1 (include) or -1 (exclude), e.g.
//
//	{"any": 1, "nav": 1}            → id + any + nav (+ narrowed _ver)
//	{"any": 1, "nav.pos": -1}       → everything of any, nav minus nav.pos
//	{"_ver": -1}                    → every user field, no version map
//
// Semantics, and the three places they diverge from mongo (all
// deliberate, all in docs/09-query.md § Projection):
//
//   - Include and exclude MIX. Mongo rejects a projection carrying
//     both; here the deepest mark on a path wins, which is what makes
//     "this subtree except one leaf" expressible.
//   - `id` is always present and CANNOT be excluded (400) — it is the
//     record identity every windowed cache and every `changes` frame
//     keys on.
//   - Protocol fields (`_`-prefixed) sit outside mode inference and
//     carry their own defaults: `_ver` is included (narrowed to the
//     projection), `_traces` / `_deletedAt` ride along whole, and
//     `_addSeq` / `_applySeq` are dropped. Each is overridden by naming
//     it explicitly. So `{"_ver": -1}` alone is still "every user
//     field", not "nothing but _ver's complement".
//
// Projection shapes output only: it never changes which records match
// or the order they arrive in.
type projection struct {
	// root holds the top-level entries as children; its own mark is
	// unused.
	root projNode
	// userInclude records whether any non-protocol path carries an
	// include mark — the mode switch. False means "start from the
	// whole record and carve", true means "start from nothing and
	// add".
	userInclude bool
	// freeform marks a projection over plain any-store documents (the
	// local store) rather than CRDT rows. Those carry no `_ver` and no
	// delivery counters, so there is no protocol namespace: an
	// `_`-prefixed name is the caller's own field, counts towards mode
	// inference, and none of the protocol defaults apply.
	freeform bool
}

// projNode is one path segment of the parsed projection tree.
// children are kept as a slice, not a map: a projection is a handful
// of entries, so a linear scan beats hashing and costs no allocation
// per lookup.
type projNode struct {
	key      string
	mark     int8 // projMarkNone / projMarkInclude / projMarkExclude
	children []projNode
	// incBelow / excBelow summarise the subtree (mark included), and
	// incChild whether any CHILD carries an inclusion — the test for
	// "a deeper mark narrows this node". Resolved once by seal() so the
	// per-record walk never re-descends just to ask.
	incBelow bool
	excBelow bool
	incChild bool
}

const (
	projMarkNone    int8 = 0
	projMarkInclude int8 = 1
	projMarkExclude int8 = -1
)

// Bounds on caller-supplied projections. A projection is a field list,
// not a scratch namespace; these only exist so a hostile body can't
// make the server walk an unbounded tree per record.
const (
	maxProjectionEntries = 128
	maxProjectionDepth   = 8
)

// verDefaultKey is the reserved key inside a `_ver` subtree holding the
// default version for any sibling not enumerated at that level (the
// SDK's crdt.defaultKey). Narrowing `_ver` KEEPS it: version lookup
// falls back to it when a key is missing, so carrying it forward is
// what makes the narrowed map answer identically to the full one for
// every projected path. A user field can never be named `*` — the SDK
// reserves it (crdt.validatePath) — so there is no ambiguity with a
// projection path.
const verDefaultKey = "*"

// idField is present on every record and never projectable away.
const idField = "id"

// verField is the per-field version map.
const verField = "_ver"

// projectionDefaultDropped are protocol fields withheld unless the
// projection names them. They are SDK-internal delivery counters —
// consumers reason with versionId — and this is the only place the
// wire drops a field the client didn't ask about, so it applies ONLY
// when a projection is present. A request without one is unchanged,
// byte for byte.
var projectionDefaultDropped = [...]string{"_addSeq", "_applySeq"}

// projectionPassthrough are protocol fields that ride along an
// include-mode record whole, unless excluded by name. `_traces` is the
// write-correlation map an optimistic client matches its own echo on
// (the `traceIds` it sent to /modify), and `_deletedAt` marks a
// tombstone: dropping either because the caller listed only user
// fields would break a client silently. `_ver` is the third, handled
// separately because it is narrowed rather than copied.
var projectionPassthrough = [...]string{"_traces", "_deletedAt"}

// parseProjection reads the request body's `projection` object. Returns
// (nil, nil, false) when absent — the no-projection path must stay
// exactly as it was.
func parseProjection(c echo.Context, root *fastjson.Value, freeform bool) (*projection, error, bool) {
	if root == nil {
		return nil, nil, false
	}
	raw := root.Get("projection")
	if raw == nil || raw.Type() == fastjson.TypeNull {
		return nil, nil, false
	}
	obj, err := raw.Object()
	if err != nil {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"projection must be a JSON object of field paths to 1 (include) or -1 (exclude)", nil), true
	}
	if obj.Len() == 0 {
		// An empty object selects nothing and excludes nothing. Treat it
		// as absent so a client that always emits the key gets the same
		// bytes as one that omits it — including the delivery counters.
		return nil, nil, false
	}
	if obj.Len() > maxProjectionEntries {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"projection carries too many entries", map[string]any{"limit": maxProjectionEntries}), true
	}
	p := &projection{freeform: freeform}
	var errResp error
	var failed bool
	obj.Visit(func(key []byte, v *fastjson.Value) {
		if failed {
			return
		}
		mark, ok := projectionMark(v)
		if !ok {
			errResp, failed = writeError(c, http.StatusBadRequest, "request.invalid_field",
				"projection value for "+string(key)+" must be 1 (include) or -1 (exclude)", nil), true
			return
		}
		errResp, failed = p.add(c, string(key), mark)
	})
	if failed {
		return nil, errResp, true
	}
	p.root.seal()
	return p, nil, false
}

// projectionMark maps an accepted projection value onto a mark. 1/true
// include, -1/0/false exclude; anything else is rejected rather than
// guessed at.
//
// Numbers go through GetFloat64, not GetInt: fastjson's GetInt parses
// best-effort and answers 0 for any non-integer literal, so `1.0` — what
// a Python client's json.dumps emits for a float 1 — would come back as
// an EXCLUDE and silently invert the projection.
func projectionMark(v *fastjson.Value) (int8, bool) {
	switch v.Type() {
	case fastjson.TypeTrue:
		return projMarkInclude, true
	case fastjson.TypeFalse:
		return projMarkExclude, true
	case fastjson.TypeNumber:
		switch n := v.GetFloat64(); n {
		case 1:
			return projMarkInclude, true
		case 0, -1:
			return projMarkExclude, true
		}
	}
	return projMarkNone, false
}

// add splits one dotted path and marks its leaf, creating intermediate
// nodes as it goes. Same (errResp, done) convention as the other
// request-boundary checks: the bool carries "handled", not the error —
// writeError answers nil once the response is written.
func (p *projection) add(c echo.Context, path string, mark int8) (error, bool) {
	if path == "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"projection path is empty", nil), true
	}
	node := &p.root
	depth := 0
	for rest := path; ; {
		var seg string
		if i := strings.IndexByte(rest, '.'); i >= 0 {
			seg, rest = rest[:i], rest[i+1:]
		} else {
			seg, rest = rest, ""
		}
		if seg == "" {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"projection path "+path+" has an empty segment", nil), true
		}
		if seg == verDefaultKey {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"projection path "+path+" uses the reserved segment \"*\"", nil), true
		}
		depth++
		if depth > maxProjectionDepth {
			return writeError(c, http.StatusBadRequest, "request.invalid_field",
				"projection path "+path+" is nested too deeply",
				map[string]any{"limit": maxProjectionDepth}), true
		}
		node = node.child(seg)
		if rest == "" {
			break
		}
	}
	top := path
	if i := strings.IndexByte(top, '.'); i >= 0 {
		top = top[:i]
	}
	// The whole `id` subtree, not just the bare path: `id` ships whole
	// and unconditionally, so `{"id.x": -1}` would be accepted and then
	// do nothing.
	if top == idField && mark == projMarkExclude {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"id cannot be excluded — it is the record identity every subscription keys on", nil), true
	}
	node.mark = mark
	if mark == projMarkInclude && (p.freeform || !strings.HasPrefix(top, "_")) {
		p.userInclude = true
	}
	return nil, false
}

// child returns the named child, appending it when missing.
func (n *projNode) child(key string) *projNode {
	for i := range n.children {
		if n.children[i].key == key {
			return &n.children[i]
		}
	}
	n.children = append(n.children, projNode{key: key})
	return &n.children[len(n.children)-1]
}

// find returns the named child or nil, without creating it.
func (n *projNode) find(key string) *projNode {
	for i := range n.children {
		if n.children[i].key == key {
			return &n.children[i]
		}
	}
	return nil
}

// seal resolves the incBelow / excBelow summaries bottom-up. Called
// once, after the whole body has been parsed.
func (n *projNode) seal() {
	n.incBelow = n.mark == projMarkInclude
	n.excBelow = n.mark == projMarkExclude
	n.incChild = false
	for i := range n.children {
		c := &n.children[i]
		c.seal()
		n.incBelow = n.incBelow || c.incBelow
		n.excBelow = n.excBelow || c.excBelow
		n.incChild = n.incChild || c.incBelow
	}
}

// record renders one stored record onto the wire under this
// projection. Everything is built into the caller's arena, which the
// handlers pool and reset per request, so the steady state allocates
// nothing.
//
// The two modes are implemented differently on purpose:
//
//   - Include mode builds up from an empty object and only ever
//     touches the named subtrees, so an unread field is never decoded
//     into fastjson and never marshalled. That is where the win is.
//   - Exclude mode converts and then carves, because the kept keys are
//     the record's own (a []byte from anyenc) and re-keying them would
//     allocate a string per field per record. It still skips the
//     marshal and the wire for what it drops.
func (p *projection) record(doc *anyenc.Value, a *fastjson.Arena) *fastjson.Value {
	if doc == nil {
		return nil
	}
	if !p.userInclude {
		out := doc.FastJson(a)
		carveNode(out, &p.root)
		if !p.freeform {
			// The version map is carved by the same exclusions, so
			// dropping a field drops its version with it.
			carveVer(out.Get(verField), &p.root)
			p.applyProtocolDefaults(out)
		}
		return out
	}

	out := a.NewObject()
	if id := doc.Get(idField); id != nil {
		out.Set(idField, id.FastJson(a))
	}
	for i := range p.root.children {
		n := &p.root.children[i]
		if n.key == idField || (!p.freeform && strings.HasPrefix(n.key, "_")) {
			continue // id is unconditional; protocol fields below
		}
		if !n.incBelow {
			continue // a bare exclusion is a no-op in include mode
		}
		if sub := doc.Get(n.key); sub != nil {
			if v := projectValue(sub, n, a); v != nil {
				out.Set(n.key, v)
			}
		}
	}
	if !p.freeform {
		p.projectProtocolFields(doc, out, a)
	}
	return out
}

// projectValue renders one subtree the projection reaches.
//
// The deepest mark decides, in both polarities. With no inclusion
// below it the node takes its whole subtree, minus any exclusions
// there — so `{"nav":1}` is all of nav and `{"nav":1,"nav.pos":-1}` is
// nav without one leaf. An inclusion below NARROWS it instead, so
// `{"nav":1,"nav.pos":1}` is nav.pos alone rather than silently all of
// nav (over-returning is the failure this whole endpoint exists to
// avoid). A node the record answers with a scalar where the projection
// wanted to descend yields nothing — "absent stays absent" rather than
// an empty object.
//
// An array is descended ELEMENT-WISE, mongo-style: `{"tags.name": 1}`
// over an array of objects keeps each element's name. An element that
// projects to nothing drops out of the array.
func projectValue(v *anyenc.Value, n *projNode, a *fastjson.Arena) *fastjson.Value {
	if !n.incChild {
		out := v.FastJson(a)
		if n.excBelow {
			carveNode(out, n)
		}
		return out
	}
	if v.Type() == anyenc.TypeArray {
		// Element-wise, and every element keeps its slot: an element
		// holding none of the projected paths comes back as {} rather
		// than vanishing, so the array's length and indices survive a
		// projection. Mongo does the same.
		out := a.NewArray()
		for i, item := range v.GetArray() {
			cv := projectValue(item, n, a)
			if cv == nil {
				cv = a.NewObject()
			}
			out.SetArrayItem(i, cv)
		}
		return out
	}
	if v.Type() != anyenc.TypeObject {
		return nil
	}
	out := a.NewObject()
	empty := true
	for i := range n.children {
		c := &n.children[i]
		if !c.incBelow {
			continue
		}
		if sub := v.Get(c.key); sub != nil {
			if cv := projectValue(sub, c, a); cv != nil {
				out.Set(c.key, cv)
				empty = false
			}
		}
	}
	if empty {
		return nil
	}
	return out
}

// carveNode deletes every excluded path of n from an already-converted
// value. Keys come from the projection, so no per-record string is
// built. Arrays are carved element-wise, matching projectValue's
// descent.
func carveNode(v *fastjson.Value, n *projNode) {
	if v == nil {
		return
	}
	if v.Type() == fastjson.TypeArray {
		for _, item := range v.GetArray() {
			carveNode(item, n)
		}
		return
	}
	for i := range n.children {
		c := &n.children[i]
		if c.mark == projMarkExclude {
			v.Del(c.key)
			continue
		}
		if len(c.children) > 0 {
			carveNode(v.Get(c.key), c)
		}
	}
}

// applyProtocolDefaults drops the default-withheld protocol fields from
// a converted record unless the projection asked for them by name.
func (p *projection) applyProtocolDefaults(v *fastjson.Value) {
	for _, key := range projectionDefaultDropped {
		if n := p.root.find(key); n != nil && n.mark == projMarkInclude {
			continue
		}
		v.Del(key)
	}
}

// projectProtocolFields adds the `_`-prefixed fields to an
// include-mode record. They sit outside mode inference: `_ver` rides
// along narrowed, the passthrough fields ride along whole, the
// delivery counters stay off unless named, and any other `_` field
// named with an include is passed through.
func (p *projection) projectProtocolFields(doc *anyenc.Value, out *fastjson.Value, a *fastjson.Arena) {
	if !p.excluded(verField) {
		if ver := doc.Get(verField); ver != nil {
			if v := projectVer(ver, &p.root, a, true); v != nil {
				carveVer(v, &p.root)
				out.Set(verField, v)
			}
		}
	}
	for _, key := range projectionPassthrough {
		if p.excluded(key) {
			continue
		}
		if sub := doc.Get(key); sub != nil {
			out.Set(key, sub.FastJson(a))
		}
	}
	for i := range p.root.children {
		n := &p.root.children[i]
		if !strings.HasPrefix(n.key, "_") || n.mark != projMarkInclude {
			continue
		}
		if n.key == verField || slices.Contains(projectionPassthrough[:], n.key) {
			continue // already placed
		}
		if sub := doc.Get(n.key); sub != nil {
			out.Set(n.key, sub.FastJson(a))
		}
	}
}

// excluded reports whether a top-level field carries an explicit
// exclude mark.
func (p *projection) excluded(key string) bool {
	n := p.root.find(key)
	return n != nil && n.mark == projMarkExclude
}

// carveVer applies the projection's EXCLUSIONS to an already-narrowed
// `_ver`, so a field the caller excluded does not leave its version
// behind. Exclusions carve; inclusions were already applied by
// projectVer. The `*` default is never a projection path, so it
// survives — see projectVer for why that matters.
func carveVer(ver *fastjson.Value, root *projNode) {
	if root.excBelow {
		carveNode(ver, root)
	}
}

// projectVer narrows the per-field version map to the projection.
//
// `_ver` mirrors the record, except that a node may be a bare version
// string (collapsed — it applies at and below that point) and an object
// node may carry `*`, the version for any sibling not enumerated
// there. Narrowing therefore keeps `*` at every level it descends into
// and copies matched subtrees verbatim.
//
// The contract that buys: for every path the projection includes, the
// narrowed map resolves to the same version as the full one. Lookup
// falls back to `*` exactly where it did before, and an included
// subtree is bit-identical. Paths the projection EXCLUDED are outside
// the contract — carveVer removes their entries afterwards, and what
// is left may resolve to a surviving `*` default rather than the
// version they had.
//
// Subtrees are never collapsed to their maximum version. That is a
// legal encoding and smaller, but it over-reports every leaf older
// than the max, and a client reconciling optimistic state per field
// would then discard a local edit that is genuinely newer.
func projectVer(ver *anyenc.Value, n *projNode, a *fastjson.Arena, root bool) *fastjson.Value {
	if ver.Type() != anyenc.TypeObject {
		// Collapsed at this level — one string covers everything below,
		// including whatever was projected.
		return ver.FastJson(a)
	}
	out := a.NewObject()
	if def := ver.Get(verDefaultKey); def != nil {
		out.Set(verDefaultKey, def.FastJson(a))
	}
	if n == nil {
		return out
	}
	if root {
		// `id` is the record's creation marker and always ships, so its
		// version does too. Only at the root: a nested object's own
		// `id` is an ordinary field and follows the projection.
		if id := ver.Get(idField); id != nil {
			out.Set(idField, id.FastJson(a))
		}
	}
	for i := range n.children {
		c := &n.children[i]
		if (root && c.key == idField) || strings.HasPrefix(c.key, "_") || !c.incBelow {
			continue
		}
		sub := ver.Get(c.key)
		if sub == nil {
			continue
		}
		// Deepest mark wins here as it does in the record body: a
		// deeper include narrows this subtree rather than being
		// subsumed by the ancestor's include.
		if !c.incChild {
			out.Set(c.key, sub.FastJson(a))
			continue
		}
		out.Set(c.key, projectVer(sub, c, a, false))
	}
	return out
}

// Verdicts for one op path against the projection.
const (
	projOpDrop   = iota // the op touches nothing the client holds
	projOpKeep          // fully inside the projection — ship verbatim
	projOpNarrow        // straddles the boundary — narrow the payload
)

// opVerdict classifies a change op's path. node is the projection node
// the walk landed on (nil when it fell off inside an included subtree,
// meaning "verbatim").
//
// The walk carries `inherited`: exclude mode starts inside the
// projection and leaves it at the first exclusion, include mode starts
// outside and enters at the first inclusion.
func (p *projection) opVerdict(path []string) (int, *projNode) {
	node := &p.root
	inherited := !p.userInclude
	for _, seg := range path {
		c := node.find(seg)
		if c == nil {
			if inherited {
				return projOpKeep, nil
			}
			return projOpDrop, nil
		}
		switch c.mark {
		case projMarkExclude:
			// Deepest mark wins here too: an exclusion only ends the
			// walk when nothing under it was included. Otherwise it
			// resets to "outside", and a deeper include lets us back in
			// — so ops stay exactly as wide as the record body.
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
