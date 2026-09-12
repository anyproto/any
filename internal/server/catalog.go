package server

import (
	"fmt"
	"sync"

	"github.com/anyproto/any-sync-sdk/handler"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/bundles"
	"github.com/anyproto/any/internal/catalog"
	"github.com/anyproto/any/internal/miniapp"
)

// The usecase catalog, compiled: every bundle body of every usecase
// turned into the bundles.Install the resolver takes, through the same
// draft converters and descriptor gate the HTTP ensure applies. The
// pure checks (ids, handles, the dependency graph, relation links,
// module rules) live in internal/catalog; this layer adds what needs
// the server's vocabulary — the slug-against-kind gate on every
// property, the part drafts, the layout, the `miniapp` values against
// the built-in's property list, and the xKeys against the registered
// type ids — and is what `make catalog-validate` runs. It does not run
// the SDK's own ensure validators (part / property drafts against the
// live module set); those answer at the first setup, which is why the
// server tests set every shipped usecase up in one space.

// compiledBundle is one catalog bundle ready to install.
type compiledBundle struct {
	api.CatalogBundle
	usecase string
	install bundles.Install
	// miniapp is the value map on the built-in `miniapp` type the root
	// carries (`bundle` filled in); nil when the bundle is no miniapp.
	miniapp map[string]any
}

// compiledUsecase is one catalog entry with its bundles compiled.
type compiledUsecase struct {
	api.CatalogUsecase
	bundles []compiledBundle
}

// compiledCatalog is the whole catalog, compiled once per process.
type compiledCatalog struct {
	cat      *catalog.Catalog
	usecases map[string]*compiledUsecase
}

// order is the usecase's transitive dependency closure in setup order,
// dependencies first, the requested usecase last.
func (cc *compiledCatalog) order(id string) ([]*compiledUsecase, bool) {
	list, ok := cc.cat.Order(id)
	if !ok {
		return nil, false
	}
	out := make([]*compiledUsecase, 0, len(list))
	for _, u := range list {
		out = append(out, cc.usecases[u.Id])
	}
	return out, true
}

// ValidateCatalog runs every check on a catalog source — the pure ones
// and the server-side gate — and returns every problem found. Nil
// means valid. Offline: nothing here touches a space or the SDK.
func ValidateCatalog(src []byte) catalog.Problems {
	_, problems := compileCatalog(src)
	return problems
}

// compileCatalog loads a catalog source and compiles it.
func compileCatalog(src []byte) (*compiledCatalog, catalog.Problems) {
	known := make([]string, 0, 8)
	for _, t := range serverTypes() {
		known = append(known, t.Id)
	}
	cat, problems := catalog.Load(src, catalog.Options{KnownTypeIds: known})
	if cat == nil {
		return nil, problems
	}
	// The structural findings and the gate's are reported together —
	// one pass for the author, whichever layer the problem is on.
	cc := &compiledCatalog{cat: cat, usecases: make(map[string]*compiledUsecase, len(cat.Usecases))}
	add := func(path, code, msg string) {
		problems = append(problems, catalog.Problem{Path: path, Code: code, Message: msg})
	}
	miniappProps := map[string]handler.PropertyDecl{}
	for _, p := range miniapp.NewType().Properties {
		miniappProps[p.Id] = p
	}
	for ui, u := range cat.Usecases {
		cu := &compiledUsecase{CatalogUsecase: u}
		for bi := range u.Bundles {
			b := u.Bundles[bi]
			bp := fmt.Sprintf("usecases[%d].bundles[%d]", ui, bi)
			cb := compiledBundle{CatalogBundle: b, usecase: u.Id}
			inst := bundles.Install{
				Id: b.Id, Name: b.Name, Derived: b.Derived, Hidden: b.Hidden,
				SelfTyped: b.SelfTyped, SystemInstall: true,
			}
			if b.Type != nil {
				inst.XKey = b.Type.XKey
				inst.Weight = b.Type.Weight
				if layout, code, reason := layoutFromWire(b.Type.Layout); code != "" {
					add(bp+".type.layout", code, reason)
				} else {
					inst.Layout = layout
				}
				for pi, req := range b.Type.Properties {
					draft, code, reason, _ := propertyDraftFromAPI(req)
					if code != "" {
						add(fmt.Sprintf("%s.type.properties[%d]", bp, pi), code, reason)
						continue
					}
					inst.Properties = append(inst.Properties, draft)
				}
			}
			for pi, req := range b.Parts {
				draft, code, reason, _ := systemPartDraftFromAPI(req)
				if code != "" {
					add(fmt.Sprintf("%s.parts[%d]", bp, pi), code, reason)
					continue
				}
				inst.Parts = append(inst.Parts, draft)
			}
			if b.Miniapp != nil {
				values := map[string]any{miniapp.PropBundle: b.Id}
				for k, v := range b.Miniapp {
					decl, ok := miniappProps[k]
					if !ok {
						add(bp+".miniapp."+k, catalog.CodeBadMiniapp, k+" is not a property of the built-in miniapp type")
						continue
					}
					if reason := miniappValueMismatch(decl, v); reason != "" {
						add(bp+".miniapp."+k, catalog.CodeBadMiniapp, reason)
						continue
					}
					values[k] = v
				}
				cb.miniapp = values
				cb.Miniapp = values
				// The listing serves what setup writes: the filled-in map,
				// not the author's shorthand (`miniapp: {}` would otherwise
				// vanish behind omitempty).
				cat.Usecases[ui].Bundles[bi].Miniapp = values
				inst.RootTypes = []string{miniapp.TypeId}
				inst.RootProperties = map[string]map[string]any{miniapp.TypeId: values}
			}
			cb.install = inst
			cu.bundles = append(cu.bundles, cb)
		}
		cu.CatalogUsecase = cat.Usecases[ui]
		cc.usecases[u.Id] = cu
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return cc, nil
}

// miniappValueMismatch reports why a catalog value does not fit a
// `miniapp` property's kind ("" = fits).
func miniappValueMismatch(decl handler.PropertyDecl, v any) string {
	switch decl.Kind {
	case handler.PropertyKindString:
		if _, ok := v.(string); ok {
			return ""
		}
		return "must be a string"
	case handler.PropertyKindNumber:
		switch v.(type) {
		case float64, float32, int, int64, int32:
			return ""
		}
		return "must be a number"
	case handler.PropertyKindBoolean:
		if _, ok := v.(bool); ok {
			return ""
		}
		return "must be a boolean"
	}
	// A kind the catalog has no value grammar for yet (array, object,
	// datetime): refused until the grammar exists, never let through.
	return "a catalog cannot seed a value of this kind"
}

// The embedded catalog, compiled once per process. Run refuses to
// boot on a problem; a handler asking later gets the same answer.
var (
	embeddedCatalogOnce sync.Once
	embeddedCatalog     *compiledCatalog
	embeddedCatalogErr  error
)

// usecaseCatalog returns the catalog this server serves: a test's
// override when set, else the compiled embedded one.
func (d *deps) usecaseCatalog() (*compiledCatalog, error) {
	if d.catalog != nil {
		return d.catalog, nil
	}
	return embeddedUsecaseCatalog()
}

// embeddedUsecaseCatalog returns the compiled embedded catalog.
func embeddedUsecaseCatalog() (*compiledCatalog, error) {
	embeddedCatalogOnce.Do(func() {
		cc, problems := compileCatalog(catalog.Embedded())
		if len(problems) > 0 {
			embeddedCatalogErr = problems
			return
		}
		embeddedCatalog = cc
	})
	return embeddedCatalog, embeddedCatalogErr
}
