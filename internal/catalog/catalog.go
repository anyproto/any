// Package catalog loads and validates the usecase catalog embedded in
// the binary (catalog.yml): the server's well-known bundles, grouped
// into usecases a client sets up with one call, dependencies included.
//
// A usecase is a set of bundles installed together plus the usecases
// it requires; every bundle is one created root under a permanent
// `system:<name>/v<n>` id, declaring a type objects carry, a miniapp
// the client opens, records on the root, or several of those. Client
// contract: docs/28-well-known-bundles.md.
//
// Validation here is structural and pure — ids, handles, the
// dependency graph, relation links, module rules — and collects every
// problem rather than the first, with its yaml path. What needs the
// server's descriptor gate (slug against kind, option shapes, miniapp
// values) is layered on top by the server package; both run at boot,
// in `make test` and in `make catalog-validate`.
package catalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/anyproto/any/internal/api"
)

//go:embed catalog.yml
var embedded []byte

// Embedded returns the catalog shipped with the binary.
func Embedded() []byte { return slices.Clone(embedded) }

// Problem is one validation finding: where, what, why.
type Problem struct {
	// Path is the yaml path of the offending node
	// (`usecases[3].bundles[1].type.properties[2].xFormat`).
	Path    string
	Code    string
	Message string
}

func (p Problem) String() string { return p.Path + ": " + p.Code + ": " + p.Message }

// Problems is the error a validation returns — every problem found,
// in document order.
type Problems []Problem

func (ps Problems) Error() string {
	lines := make([]string, len(ps))
	for i, p := range ps {
		lines[i] = p.String()
	}
	return fmt.Sprintf("catalog: %d problem(s):\n%s", len(ps), strings.Join(lines, "\n"))
}

// Problem codes.
const (
	CodeBadYAML        = "catalog.bad_yaml"
	CodeUnknownField   = "catalog.unknown_field"
	CodeBadId          = "catalog.bad_id"
	CodeDuplicate      = "catalog.duplicate"
	CodeMissing        = "catalog.missing"
	CodeBadField       = "catalog.bad_field"
	CodeUnknownUsecase = "catalog.unknown_usecase"
	CodeCycle          = "catalog.cycle"
	CodeBrokenLink     = "catalog.broken_link"
	CodeBadMiniapp     = "catalog.bad_miniapp"
)

// Options tune the pure validation with what only the server knows.
type Options struct {
	// KnownTypeIds are the registered (built-in) type and collection
	// ids of the server: reserved against catalog xKeys, and valid
	// relation targets. `any`, `type` and `collection` are always
	// included.
	KnownTypeIds []string
	// RootTypeIds are the registered type ids a bare bundle root may
	// name as its `rootType`.
	RootTypeIds []string
}

// Catalog is a loaded, structurally valid catalog.
type Catalog struct {
	Usecases []api.CatalogUsecase
	byId     map[string]int
	// types maps a type's or collection's xKey to the usecase
	// declaring it.
	types map[string]string
	// superseded holds the id of every bundle another one supersedes.
	superseded map[string]bool
	// groups maps every bundle linked by `supersedes` to its group.
	groups map[string]*SupersedeGroup
}

// Superseded reports whether another bundle of the catalog supersedes
// the one with this id: it is kept where a space already has it and
// never what a new space receives.
func (c *Catalog) Superseded(bundleId string) bool { return c.superseded[bundleId] }

// SupersedeGroup is one connected set of bundles linked by
// `supersedes`: the superseded ones (Old) and the ones standing in for
// them (New), each in declaration order. A space is on the group's old
// shape while any bundle of Old is installed — it then walks Old and
// never receives New — and on the new shape otherwise. One group per
// component, so a bundle superseded by two others and a bundle
// superseding two others pull all of them into one decision.
type SupersedeGroup struct {
	Old []string
	New []string
}

// OldKept reports whether the space is on the old shape: any bundle of
// Old is among the installed ids.
func (g *SupersedeGroup) OldKept(installed map[string]bool) bool {
	for _, id := range g.Old {
		if installed[id] {
			return true
		}
	}
	return false
}

// NewAny reports whether the space is on the new shape already: any
// bundle of New is among the installed ids. One is proof enough — the
// two shapes never meet in a space.
func (g *SupersedeGroup) NewAny(installed map[string]bool) bool {
	for _, id := range g.New {
		if installed[id] {
			return true
		}
	}
	return false
}

// SupersedeGroup returns the group the bundle belongs to, nil when no
// `supersedes` edge touches it.
func (c *Catalog) SupersedeGroup(bundleId string) *SupersedeGroup { return c.groups[bundleId] }

// Get returns the usecase with the id.
func (c *Catalog) Get(id string) (api.CatalogUsecase, bool) {
	i, ok := c.byId[id]
	if !ok {
		return api.CatalogUsecase{}, false
	}
	return c.Usecases[i], true
}

// Order returns the usecase's transitive dependency closure in setup
// order — dependencies first, each once, the requested usecase last.
// Deterministic: requires are walked in declaration order.
func (c *Catalog) Order(id string) ([]api.CatalogUsecase, bool) {
	if _, ok := c.byId[id]; !ok {
		return nil, false
	}
	var out []api.CatalogUsecase
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		u := c.Usecases[c.byId[id]]
		for _, r := range u.Requires {
			walk(r)
		}
		out = append(out, u)
	}
	walk(id)
	return out, true
}

// Load decodes a catalog and runs the pure validation, returning every
// finding. The catalog is nil only when the source did not decode; with
// structural problems it is still returned, so a caller layering more
// checks on top can report everything in one pass — but Get and Order
// are trustworthy only when Problems is empty.
func Load(src []byte, opts Options) (*Catalog, Problems) {
	doc, problems := decode(src)
	if len(problems) > 0 {
		return nil, problems
	}
	cat := &Catalog{Usecases: doc.Usecases, byId: map[string]int{}, types: map[string]string{}}
	return cat, cat.validate(opts)
}

// document is the yaml root.
type document struct {
	Usecases []api.CatalogUsecase `json:"usecases"`
}

var (
	slugRe   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	xKeyRe   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	propRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	bundleRe = regexp.MustCompile(`^system:[a-z][a-z0-9-]*/v[1-9][0-9]*$`)
)

// BundleIdPattern is the grammar of a catalog bundle id, for messages.
const BundleIdPattern = "system:<name>/v<n>"

// Bounds — the same the per-space ensure applies (bundle records are
// permanent and ride the eagerly-loaded space index on every device).
const (
	maxNameBytes  = 1024
	maxParts      = 32
	maxProperties = 64
)

// decode parses the yaml, refuses unknown keys against the api structs
// (with their path), and produces the typed document.
func decode(src []byte) (document, Problems) {
	var raw any
	if err := yaml.Unmarshal(src, &raw); err != nil {
		return document{}, Problems{{Path: "", Code: CodeBadYAML, Message: err.Error()}}
	}
	if raw == nil {
		return document{}, Problems{{Path: "", Code: CodeMissing, Message: "empty catalog"}}
	}
	var problems Problems
	// Non-string mapping keys first, over the whole tree — they hide
	// inside free-form descriptors the typed walk does not enter, and
	// the JSON bridge below cannot carry them.
	nonStringKeys("", raw, &problems)
	unknownKeys("", raw, reflect.TypeOf(document{}), &problems)
	if len(problems) > 0 {
		return document{}, problems
	}
	buf, err := json.Marshal(raw)
	if err != nil {
		return document{}, Problems{{Path: "", Code: CodeBadYAML, Message: err.Error()}}
	}
	var doc document
	dec := json.NewDecoder(bytes.NewReader(buf))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return document{}, Problems{{Path: "", Code: CodeBadYAML, Message: err.Error()}}
	}
	return doc, nil
}

var unmarshalerType = reflect.TypeFor[json.Unmarshaler]()

// unknownKeys walks the decoded yaml against the json field names of
// the target struct types, reporting every key no struct declares
// with its path. Maps (`json.RawMessage`, `map[string]any`) and types
// with their own decoding (`json.Unmarshaler`, such as a search field
// that reads a bare string) are free-form and not descended. A mapping
// with a non-string key (an unquoted number or boolean) is reported
// where it sits — yaml keeps it as a Go map the JSON bridge cannot
// carry.
func unknownKeys(path string, v any, t reflect.Type, out *Problems) {
	if _, bad := v.(map[any]any); bad {
		return // reported by nonStringKeys
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(unmarshalerType) {
		return
	}
	switch t.Kind() {
	case reflect.Struct:
		obj, ok := v.(map[string]any)
		if !ok {
			if v != nil {
				*out = append(*out, Problem{Path: path, Code: CodeBadField, Message: "must be a mapping"})
			}
			return
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields[name] = f.Type
		}
		for _, k := range sortedKeys(obj) {
			ft, ok := fields[k]
			if !ok {
				*out = append(*out, Problem{Path: join(path, k), Code: CodeUnknownField, Message: "unknown key " + k})
				continue
			}
			unknownKeys(join(path, k), obj[k], ft, out)
		}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 { // json.RawMessage
			return
		}
		arr, ok := v.([]any)
		if !ok {
			if v != nil {
				*out = append(*out, Problem{Path: path, Code: CodeBadField, Message: "must be a list"})
			}
			return
		}
		for i, e := range arr {
			unknownKeys(fmt.Sprintf("%s[%d]", path, i), e, t.Elem(), out)
		}
	}
}

// nonStringKeys reports every mapping whose keys are not all strings
// (an unquoted number or boolean as an option key), with the path of
// the mapping. yaml keeps such a mapping as a Go map keyed by `any`,
// which the JSON bridge refuses without a location.
func nonStringKeys(path string, v any, out *Problems) {
	switch x := v.(type) {
	case map[any]any:
		*out = append(*out, Problem{Path: path, Code: CodeBadField, Message: "mapping keys must be strings — quote a numeric or boolean key"})
	case map[string]any:
		for _, k := range sortedKeys(x) {
			nonStringKeys(join(path, k), x[k], out)
		}
	case []any:
		for i, e := range x {
			nonStringKeys(fmt.Sprintf("%s[%d]", path, i), e, out)
		}
	}
}

func join(path, k string) string {
	if path == "" {
		return k
	}
	return path + "." + k
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// validate runs every pure check and fills the lookup maps.
func (c *Catalog) validate(opts Options) Problems {
	var ps Problems
	add := func(path, code, msg string) { ps = append(ps, Problem{Path: path, Code: code, Message: msg}) }

	known := map[string]bool{"any": true, "type": true, "collection": true}
	for _, id := range opts.KnownTypeIds {
		known[id] = true
	}
	rootTypes := map[string]bool{}
	for _, id := range opts.RootTypeIds {
		rootTypes[id] = true
	}
	if len(c.Usecases) == 0 {
		add("usecases", CodeMissing, "at least one usecase")
	}
	bundleIds := map[string]string{}
	xkeys := map[string][]string{}
	// supersedes[a][b]: bundle a stands in for bundle b. Read before the
	// walk because the handle check needs it for a bundle declared
	// earlier than the one that supersedes it.
	supersedes := map[string]map[string]bool{}
	superseded := map[string]bool{}
	for ui := range c.Usecases {
		for bi, b := range c.Usecases[ui].Bundles {
			if b.Superseded {
				add(fmt.Sprintf("usecases[%d].bundles[%d].superseded", ui, bi), CodeBadField,
					"superseded is derived from the other bundles' supersedes — not declared")
			}
			for _, old := range b.Supersedes {
				if supersedes[b.Id] == nil {
					supersedes[b.Id] = map[string]bool{}
				}
				supersedes[b.Id][old] = true
				superseded[old] = true
			}
		}
	}
	c.superseded = superseded
	c.groupSupersedes(supersedes)
	for ui := range c.Usecases {
		for bi := range c.Usecases[ui].Bundles {
			b := &c.Usecases[ui].Bundles[bi]
			b.Superseded = superseded[b.Id]
		}
	}
	sharesHandle := func(a, b string) bool { return supersedes[a][b] || supersedes[b][a] }
	for ui := range c.Usecases {
		u := &c.Usecases[ui]
		up := fmt.Sprintf("usecases[%d]", ui)
		if !slugRe.MatchString(u.Id) {
			add(up+".id", CodeBadId, fmt.Sprintf("%q is not a slug ([a-z][a-z0-9-]*)", u.Id))
		} else if _, dup := c.byId[u.Id]; dup {
			add(up+".id", CodeDuplicate, "usecase "+u.Id+" declared twice")
		} else {
			c.byId[u.Id] = ui
		}
		if u.Name == "" {
			add(up+".name", CodeMissing, "name required")
		}
		if len(u.Bundles) == 0 {
			add(up+".bundles", CodeMissing, "at least one bundle")
		}
		seenReq := map[string]bool{}
		for ri, r := range u.Requires {
			rp := fmt.Sprintf("%s.requires[%d]", up, ri)
			if r == u.Id {
				add(rp, CodeCycle, "a usecase cannot require itself")
			}
			if seenReq[r] {
				add(rp, CodeDuplicate, "required twice: "+r)
			}
			seenReq[r] = true
		}
		for bi := range u.Bundles {
			b := &u.Bundles[bi]
			bp := fmt.Sprintf("%s.bundles[%d]", up, bi)
			if !bundleRe.MatchString(b.Id) {
				add(bp+".id", CodeBadId, fmt.Sprintf("%q must match %s", b.Id, BundleIdPattern))
			} else if prev, dup := bundleIds[b.Id]; dup {
				add(bp+".id", CodeDuplicate, "bundle "+b.Id+" also declared by usecase "+prev)
			} else {
				bundleIds[b.Id] = u.Id
			}
			if b.Name == "" {
				add(bp+".name", CodeMissing, "name required")
			} else if len(b.Name) > maxNameBytes {
				add(bp+".name", CodeBadField, fmt.Sprintf("name longer than %d bytes", maxNameBytes))
			}
			if len(b.Parts) > maxParts {
				add(bp+".parts", CodeBadField, fmt.Sprintf("more than %d parts", maxParts))
			}
			if b.Type != nil && len(b.Type.Properties) > maxProperties {
				add(bp+".type.properties", CodeBadField, fmt.Sprintf("more than %d properties", maxProperties))
			}
			if b.Collection != nil && len(b.Collection.Properties) > maxProperties {
				add(bp+".collection.properties", CodeBadField, fmt.Sprintf("more than %d properties", maxProperties))
			}
			declares := b.Type != nil || b.Collection != nil || len(b.Parts) > 0
			if !declares && b.Miniapp == nil {
				add(bp, CodeMissing, "a bundle declares at least one of type, collection, miniapp, parts")
			}
			if b.Type != nil && b.Collection != nil {
				add(bp+".collection", CodeBadField, "a root defines a type or a collection, not both")
			}
			if b.Collection != nil && len(b.Parts) > 0 {
				add(bp+".parts", CodeBadField, "a collection declares no parts")
			}
			if b.Hidden && !declares {
				add(bp+".hidden", CodeBadField, "hidden describes a definition — needs type, collection or parts")
			}
			switch {
			case declares && b.RootType != "":
				add(bp+".rootType", CodeBadField, "a declaring root carries its marker in any.type — no rootType")
			case !declares && b.RootType == "":
				add(bp+".rootType", CodeMissing, "a root that declares nothing needs a rootType (page for a plain document)")
			case !declares && !rootTypes[b.RootType]:
				add(bp+".rootType", CodeBadField, b.RootType+" is not a registered type")
			}
			if b.Type != nil {
				c.validateType(bp+".type", u.Id, b, known, xkeys, sharesHandle, add)
				// A type is a format: it brings a layout or a part. A
				// definition that only adds properties is a collection. A
				// superseded bundle is exempt — it is the shape spaces that
				// already have it keep, never what a new space receives.
				if !superseded[b.Id] && !hasLayout(b.Type.Layout) && len(b.Parts) == 0 {
					add(bp+".type", CodeBadField,
						"a type declares a layout or a part — a definition that only adds properties is a collection")
				}
			}
			if b.Collection != nil {
				c.validateCollection(bp+".collection", u.Id, b, known, xkeys, sharesHandle, add)
				validateMeta(bp+".collection.meta", b.Collection.Meta, add)
			}
			seenOld := map[string]bool{}
			for si, old := range b.Supersedes {
				sp := fmt.Sprintf("%s.supersedes[%d]", bp, si)
				switch {
				case old == b.Id:
					add(sp, CodeCycle, "a bundle cannot supersede itself")
				case seenOld[old]:
					add(sp, CodeDuplicate, "superseded twice: "+old)
				case !inUsecase(u, old):
					add(sp, CodeBrokenLink, old+" is not a bundle of usecase "+u.Id)
				case len(supersedes[old]) > 0:
					add(sp, CodeBadField, old+" supersedes a bundle itself — one step only, no chains")
				}
				seenOld[old] = true
			}
			if b.Miniapp != nil {
				if v, ok := b.Miniapp["bundle"]; ok {
					if s, isStr := v.(string); !isStr || s != b.Id {
						add(bp+".miniapp.bundle", CodeBadMiniapp, fmt.Sprintf("must equal the bundle id %s", b.Id))
					}
				}
			}
			validateParts(bp+".parts", b.Parts, add)
		}
	}

	// Dependencies: every requires resolves, the graph is acyclic.
	for ui := range c.Usecases {
		u := &c.Usecases[ui]
		for ri, r := range u.Requires {
			if _, ok := c.byId[r]; !ok {
				add(fmt.Sprintf("usecases[%d].requires[%d]", ui, ri), CodeUnknownUsecase, "no usecase "+r)
			}
		}
	}
	c.findCycles(add)

	// Links: every relation target is a type in the usecase, in its
	// transitive requires, or a known built-in.
	typeKeys := map[string]bool{}
	for ui := range c.Usecases {
		for _, b := range c.Usecases[ui].Bundles {
			if b.Type != nil {
				typeKeys[b.Type.XKey] = true
			}
		}
	}
	for ui := range c.Usecases {
		u := &c.Usecases[ui]
		if _, ok := c.byId[u.Id]; !ok {
			continue
		}
		reach := map[string]bool{}
		for _, dep := range c.closure(u.Id) {
			for xk, owner := range c.types {
				if owner == dep {
					reach[xk] = true
				}
			}
		}
		for bi := range u.Bundles {
			b := &u.Bundles[bi]
			// meta.defaultType is the type rows get: a type xKey the
			// space holds after setup, or a registered type. A handle
			// that resolves to nothing degrades every row to page.
			if b.Collection != nil {
				if dt, ok := b.Collection.Meta["defaultType"]; ok {
					dp := fmt.Sprintf("usecases[%d].bundles[%d].collection.meta.defaultType", ui, bi)
					xk, isStr := dt.(string)
					switch {
					case !isStr:
						add(dp, CodeBadField, "defaultType is a type xKey")
					case rootTypes[xk] || (reach[xk] && typeKeys[xk]):
					case c.types[xk] != "" && !typeKeys[xk]:
						add(dp, CodeBrokenLink, "defaultType "+xk+" is a collection — rows need a type")
					case c.types[xk] != "":
						add(dp, CodeBrokenLink, "defaultType "+xk+" is declared by usecase "+c.types[xk]+": add it to requires")
					default:
						add(dp, CodeBrokenLink, "defaultType "+xk+" is no type of this usecase, its requires or a built-in")
					}
				}
			}
			props, group := declaredProperties(b)
			if props == nil {
				continue
			}
			for pi := range props {
				pr := &props[pi]
				pp := fmt.Sprintf("usecases[%d].bundles[%d].%s.properties[%d].xFormat.relation.targetTypes", ui, bi, group, pi)
				for ti, target := range relationTargets(pr.XFormat) {
					if reach[target] || known[target] {
						continue
					}
					msg := "targetTypes " + target + " is not a type of this usecase or its requires"
					if owner, ok := c.types[target]; ok {
						msg += " — declared by usecase " + owner + ": add it to requires"
					} else {
						msg += " — no such type in the catalog"
					}
					add(fmt.Sprintf("%s[%d]", pp, ti), CodeBrokenLink, msg)
				}
			}
		}
	}
	return ps
}

// declaredProperties returns the property drafts a bundle declares and
// the yaml group they sit under ("type" or "collection"); nil when
// the bundle declares neither.
func declaredProperties(b *api.CatalogBundle) ([]api.AddPropertyRequest, string) {
	switch {
	case b.Type != nil:
		return b.Type.Properties, "type"
	case b.Collection != nil:
		return b.Collection.Properties, "collection"
	}
	return nil, ""
}

func (c *Catalog) validateType(tp, usecase string, b *api.CatalogBundle, known map[string]bool,
	xkeys map[string][]string, sharesHandle func(a, b string) bool, add func(path, code, msg string)) {
	c.validateHandle(tp, usecase, b.Id, b.Type.XKey, known, xkeys, sharesHandle, add)
	c.validateProperties(tp, b.Type.Properties, add)
}

// hasLayout reports whether a type declaration carries a layout.
// groupSupersedes builds the supersede groups: the connected
// components of the `supersedes` edges, members in declaration order.
func (c *Catalog) groupSupersedes(supersedes map[string]map[string]bool) {
	parent := map[string]string{}
	var find func(string) string
	find = func(id string) string {
		if p, ok := parent[id]; ok && p != id {
			parent[id] = find(p)
			return parent[id]
		}
		if _, ok := parent[id]; !ok {
			parent[id] = id
		}
		return id
	}
	for id, olds := range supersedes {
		for old := range olds {
			parent[find(old)] = find(id)
		}
	}
	c.groups = map[string]*SupersedeGroup{}
	byRoot := map[string]*SupersedeGroup{}
	for ui := range c.Usecases {
		for _, b := range c.Usecases[ui].Bundles {
			if _, linked := parent[b.Id]; !linked {
				continue
			}
			root := find(b.Id)
			g := byRoot[root]
			if g == nil {
				g = &SupersedeGroup{}
				byRoot[root] = g
			}
			if c.superseded[b.Id] {
				g.Old = append(g.Old, b.Id)
			} else {
				g.New = append(g.New, b.Id)
			}
			c.groups[b.Id] = g
		}
	}
}

func hasLayout(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func inUsecase(u *api.CatalogUsecase, bundleId string) bool {
	for i := range u.Bundles {
		if u.Bundles[i].Id == bundleId {
			return true
		}
	}
	return false
}

// validateMeta checks a definition's flag bag against the grammar the
// definition routes enforce (api.CheckMetaEntry): single-level keys,
// scalar values, no nil — a catalog declares values, never clears.
func validateMeta(mp string, meta map[string]any, add func(path, code, msg string)) {
	for k, v := range meta {
		if reason := api.CheckMetaEntry(k, v, false); reason != "" {
			add(mp+"."+k, CodeBadField, reason)
		}
	}
}

// validateCollection applies the type rules minus layout: a handle and
// columns.
func (c *Catalog) validateCollection(cp, usecase string, b *api.CatalogBundle, known map[string]bool,
	xkeys map[string][]string, sharesHandle func(a, b string) bool, add func(path, code, msg string)) {
	c.validateHandle(cp, usecase, b.Id, b.Collection.XKey, known, xkeys, sharesHandle, add)
	c.validateProperties(cp, b.Collection.Properties, add)
}

// validateHandle checks a declaration's xKey: a handle, not a built-in
// id, unique across the catalog's types and collections together. The
// one exception is a bundle and the bundle it supersedes: a space holds
// one of the two, never both, so the handle still names one definition.
func (c *Catalog) validateHandle(tp, usecase, bundleId, xKey string, known map[string]bool,
	xkeys map[string][]string, sharesHandle func(a, b string) bool, add func(path, code, msg string)) {
	if !xKeyRe.MatchString(xKey) {
		add(tp+".xKey", CodeBadId, fmt.Sprintf("%q is not a handle ([a-z][a-z0-9_]*)", xKey))
	} else if known[xKey] {
		add(tp+".xKey", CodeDuplicate, xKey+" is a built-in id")
	} else if prev := holderNotSharing(xkeys[xKey], bundleId, sharesHandle); prev != "" {
		add(tp+".xKey", CodeDuplicate, "xKey "+xKey+" also on bundle "+prev)
	} else {
		xkeys[xKey] = append(xkeys[xKey], bundleId)
		c.types[xKey] = usecase
	}
}

// holderNotSharing returns the first holder of a handle the claimant
// may not share it with: one that neither supersedes it nor is
// superseded by it. Checked against every holder, so two bundles that
// both supersede a third cannot both claim its handle.
func holderNotSharing(holders []string, bundleId string, sharesHandle func(a, b string) bool) string {
	for _, h := range holders {
		if !sharesHandle(bundleId, h) {
			return h
		}
	}
	return ""
}

func (c *Catalog) validateProperties(tp string, properties []api.AddPropertyRequest, add func(path, code, msg string)) {
	seen := map[string]bool{}
	for pi := range properties {
		pr := &properties[pi]
		pp := fmt.Sprintf("%s.properties[%d]", tp, pi)
		switch {
		case pr.XKey == "":
			add(pp+".xKey", CodeMissing, "property xKey required — its id derives from it")
		case !propRe.MatchString(pr.XKey):
			add(pp+".xKey", CodeBadId, fmt.Sprintf("%q is not a handle ([A-Za-z][A-Za-z0-9_]*)", pr.XKey))
		case seen[pr.XKey]:
			add(pp+".xKey", CodeDuplicate, "property "+pr.XKey+" declared twice")
		}
		seen[pr.XKey] = true
		if pr.Kind == "" {
			add(pp+".kind", CodeMissing, "kind required")
		}
		for k := range pr.Meta {
			if k != "index" {
				add(pp+".meta."+k, CodeBadField, "meta carries only index")
			}
		}
		if xf := xformatMap(pr.XFormat); xf != nil {
			if rel, ok := xf["relation"].(map[string]any); ok {
				if _, hasFilter := rel["filter"]; hasFilter {
					add(pp+".xFormat.relation.filter", CodeBadField, "a filter does not survive installation into another space")
				}
				if xf["type"] == "relation" && len(relationTargets(pr.XFormat)) == 0 {
					add(pp+".xFormat.relation.targetTypes", CodeMissing, "a relation names its target types")
				}
			} else if xf["type"] == "relation" {
				add(pp+".xFormat.relation", CodeMissing, "a relation names its target types")
			}
		}
	}
}

// validateParts applies the module rules the server would refuse at
// setup, with paths.
func validateParts(pp string, parts []api.PartDraftRequest, add func(path, code, msg string)) {
	partKeys, datasetKeys := map[string]bool{}, map[string]bool{}
	for pi := range parts {
		part := &parts[pi]
		p := fmt.Sprintf("%s[%d]", pp, pi)
		if !xKeyRe.MatchString(part.Key) {
			add(p+".key", CodeBadId, fmt.Sprintf("%q is not a part key ([a-z][a-z0-9_]*)", part.Key))
		} else if partKeys[part.Key] {
			add(p+".key", CodeDuplicate, "part "+part.Key+" declared twice")
		}
		partKeys[part.Key] = true
		for di := range part.Datasets {
			ds := &part.Datasets[di]
			dp := fmt.Sprintf("%s.datasets[%d]", p, di)
			module := ds.Module
			if module == "" {
				module = "records"
			}
			switch module {
			case "records", "editor", "chat":
			default:
				add(dp+".module", CodeBadField, "module is records, editor or chat")
			}
			if module == "chat" && !ds.Shared {
				add(dp, CodeBadField, "chat is shared only")
			}
			if module == "records" && ds.Shared {
				add(dp, CodeBadField, "records is never shared")
			}
			if module != "records" && len(ds.Fields) > 0 {
				add(dp+".fields", CodeBadField, "fields only on a records dataset — the module owns the schema")
			}
			key := ds.Key
			if key == "" && ds.Shared {
				key = module
			}
			switch {
			case key == "":
				add(dp+".key", CodeMissing, "key required on a namespaced dataset")
			case datasetKeys[key]:
				add(dp+".key", CodeDuplicate, "dataset "+key+" declared twice")
			}
			datasetKeys[key] = true
			// Author gates need somebody to compare against: the SDK
			// refuses a declaration where an author-only delete OR an
			// author-mutable field has no creator stamp. Catch both here
			// or the first setup in a real space is where it surfaces.
			creator, authorMutable := false, ""
			declared := map[string]bool{}
			for _, f := range ds.Fields {
				if f.Stamp == "creator" {
					creator = true
				}
				if f.MutableBy == "author" && authorMutable == "" {
					authorMutable = f.Key
				}
				declared[f.Key] = true
			}
			if !creator {
				if ds.DeleteBy == "author" {
					add(dp+".deleteBy", CodeBadField, "deleteBy author needs a field with stamp creator")
				}
				if authorMutable != "" {
					add(dp+".fields", CodeBadField,
						"mutableBy author on "+authorMutable+" needs a field with stamp creator")
				}
			}
			// A search mapping that names a field the dataset does not
			// declare indexes nothing, silently.
			if ds.Search != nil && len(ds.Fields) > 0 {
				if t := ds.Search.Title; t != "" && !declared[t] {
					add(dp+".search.title", CodeBadField, "no field "+t+" on this dataset")
				}
				for _, t := range ds.Search.Text {
					if !declared[t] {
						add(dp+".search.text", CodeBadField, "no field "+t+" on this dataset")
					}
				}
			}
		}
	}
}

// findCycles reports every cycle in the requires graph as its path.
func (c *Catalog) findCycles(add func(path, code, msg string)) {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	reported := map[string]bool{}
	var stack []string
	var visit func(id string)
	visit = func(id string) {
		color[id] = grey
		stack = append(stack, id)
		u := c.Usecases[c.byId[id]]
		for _, r := range u.Requires {
			if _, ok := c.byId[r]; !ok {
				continue
			}
			switch color[r] {
			case grey:
				start := slices.Index(stack, r)
				cycle := append(slices.Clone(stack[start:]), r)
				key := strings.Join(cycle, ">")
				if !reported[key] {
					reported[key] = true
					add(fmt.Sprintf("usecases[%d].requires", c.byId[id]), CodeCycle, strings.Join(cycle, " → "))
				}
			case white:
				visit(r)
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
	}
	for _, u := range c.Usecases {
		if _, ok := c.byId[u.Id]; ok && color[u.Id] == white {
			visit(u.Id)
		}
	}
}

// closure is the usecase plus its transitive requires (acyclic or not
// — a cycle is reported separately and terminates here on the seen
// set).
func (c *Catalog) closure(id string) []string {
	seen := map[string]bool{}
	var out []string
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
		if i, ok := c.byId[id]; ok {
			for _, r := range c.Usecases[i].Requires {
				walk(r)
			}
		}
	}
	walk(id)
	return out
}

func xformatMap(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var xf map[string]any
	if err := json.Unmarshal(raw, &xf); err != nil {
		return nil
	}
	return xf
}

// relationTargets reads relation.targetTypes off a descriptor
// (strings only; the descriptor gate reports other shapes).
func relationTargets(raw json.RawMessage) []string {
	xf := xformatMap(raw)
	if xf == nil {
		return nil
	}
	rel, ok := xf["relation"].(map[string]any)
	if !ok {
		return nil
	}
	list, ok := rel["targetTypes"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
