package server

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"
)

// The account's tech space is a valid :spaceId on the per-space routes
// for the surfaces account-level bundles need: bundles, type reads and
// dataset declarations on bundle roots, records on bundle roots,
// GET space, sync-status, debug. Everything else is refused up front
// with one code — the SDK's restricted handle refuses the same set
// with space.ErrUnsupported, mapped to the same code as a backstop.

const (
	codeSpaceUnsupported = "space.unsupported"
	codeObjectDeleted    = "object.deleted"
)

// techIndexDatasetPolicy is the ONE table for the tech index object's
// generic-read surface: which of its datasets are reachable at all,
// and which row fields are withheld (guest / issued-invite PRIVATE
// keys on `spaces` rows). `identities` rows carry a synced symKey and
// stay behind GET /v1/identities; the remaining system datasets are
// typed-API-only. The account-level /v1/spaces/query keeps its own
// narrower allowlist (no `bundles`) in handlers_spaces_query.go but
// shares the strip list.
var techIndexDatasetPolicy = map[string][]string{
	SpaceListDataset: spaceListStrippedFields,
	"profile":        nil,
	"bundles":        nil,
}

func (d *deps) isTechSpace(spaceId string) bool {
	return spaceId != "" && d.sdk != nil && spaceId == d.sdk.TechSpaceId()
}

func unsupportedOnTechSpace(c echo.Context, spaceId string) error {
	return writeError(c, http.StatusMethodNotAllowed, codeSpaceUnsupported,
		"not available on the tech space", map[string]any{"spaceId": spaceId})
}

// techAllowedRoutes is the closed set of per-space routes that serve
// the tech space, keyed "METHOD <registered pattern>". Everything
// else with a :spaceId naming the tech space is refused by
// techSpaceRouteGuard before its handler runs — a new route is
// tech-refused until someone lists it here.
var techAllowedRoutes = map[string]struct{}{
	"GET /v1/spaces/:spaceId":          {},
	"POST /v1/spaces/:spaceId/sync":    {},
	"GET /v1/spaces/:spaceId/datasets": {},

	"POST /v1/spaces/:spaceId/query":                   {},
	"POST /v1/spaces/:spaceId/query/subscribe":         {},
	"POST /v1/spaces/:spaceId/aggregate":               {},
	"POST /v1/spaces/:spaceId/modify":                  {},
	"POST /v1/spaces/:spaceId/upsert":                  {},
	"POST /v1/spaces/:spaceId/delete-records":          {},
	"POST /v1/spaces/:spaceId/objects/query":           {},
	"POST /v1/spaces/:spaceId/objects/query/subscribe": {},
	"POST /v1/spaces/:spaceId/objects/aggregate":       {},
	"GET /v1/spaces/:spaceId/objects/:objectId":        {},

	"GET /v1/spaces/:spaceId/types":                                            {},
	"GET /v1/spaces/:spaceId/types/:typeId":                                    {},
	"GET /v1/spaces/:spaceId/types/:typeId/properties":                         {},
	"GET /v1/spaces/:spaceId/types/:typeId/datasets":                           {},
	"POST /v1/spaces/:spaceId/types/:typeId/datasets":                          {},
	"PATCH /v1/spaces/:spaceId/types/:typeId/datasets/:defId":                  {},
	"DELETE /v1/spaces/:spaceId/types/:typeId/datasets/:defId":                 {},
	"POST /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields":            {},
	"PATCH /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId":  {},
	"DELETE /v1/spaces/:spaceId/types/:typeId/datasets/:defId/fields/:fieldId": {},

	// children stay off the list (phase-2, not exposed). resolve is
	// load-bearing: created installs can fork across offline devices,
	// and the loser must be resolvable here like in any space. DELETE
	// objects is the uninstall path — the SDK permits it for bundle
	// roots only and refuses everything else.
	"POST /v1/spaces/:spaceId/bundles":                   {},
	"GET /v1/spaces/:spaceId/bundles":                    {},
	"GET /v1/spaces/:spaceId/bundles/:bundleId":          {},
	"POST /v1/spaces/:spaceId/bundles/:bundleId/resolve": {},
	"DELETE /v1/spaces/:spaceId/objects/:objectId":       {},

	"GET /v1/spaces/:spaceId/sync-status":                             {},
	"GET /v1/spaces/:spaceId/sync-status/objects/:objectId":           {},
	"GET /v1/spaces/:spaceId/sync-status/objects/:objectId/subscribe": {},
	"GET /v1/spaces/:spaceId/debug":                                   {},
	"GET /v1/spaces/:spaceId/debug/objects/:objectId":                 {},
}

// techSpaceRouteGuard fails closed: a per-space route serving the
// tech space must be on techAllowedRoutes. Runs for every /v1 route;
// routes without a :spaceId param (or with another space's id) pass
// untouched.
func (d *deps) techSpaceRouteGuard(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("spaceId")
		if !d.isTechSpace(id) {
			return next(c)
		}
		if _, ok := techAllowedRoutes[c.Request().Method+" "+c.Path()]; !ok {
			return unsupportedOnTechSpace(c, id)
		}
		return next(c)
	}
}

// techIndexVet applies techIndexDatasetPolicy to a per-object read on
// the tech space: the allowlist, the field strip, and a refusal of
// filters/sorts that touch a stripped field — filtering on withheld
// data is a byte-by-byte oracle. Returns nil (skip the vet entirely)
// for a regular space.
func (d *deps) techIndexVet(c echo.Context, sp space.Space) perObjectVet {
	if !d.isTechSpace(sp.Id()) {
		return nil
	}
	indexId := sp.SpaceIndexObjectId()
	return func(root *fastjson.Value, objectId, dataset string) ([]string, error, bool) {
		return d.techIndexFence(c, root, indexId, objectId, dataset)
	}
}

func (d *deps) techIndexFence(c echo.Context, root *fastjson.Value, indexId, objectId, dataset string) (strip []string, errResp error, done bool) {
	if objectId != indexId {
		return nil, nil, false
	}
	return vetIndexDatasetRead(c, root, dataset, techIndexDatasetPolicy,
		" on the tech index object (read identities via GET /v1/identities)",
		map[string]any{"objectId": objectId, "dataset": dataset})
}

// vetIndexDatasetRead is the ONE read-side vet for index-object
// datasets, shared by the tech-space fence and the account-level
// space-list query: allowlist membership plus no filter/sort touching
// withheld fields. Allowlist, strip lists and the refusal message all
// derive from the policy table, so a policy edit cannot leave a route
// behind.
func vetIndexDatasetRead(c echo.Context, root *fastjson.Value, dataset string, policy map[string][]string, note string, details map[string]any) (strip []string, errResp error, done bool) {
	stripped, ok := policy[dataset]
	if !ok {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"dataset must be one of: "+strings.Join(policyDatasets(policy, false), ", ")+note,
			details), true
	}
	if len(stripped) > 0 && root != nil && queryTouchesFields(root, stripped) {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"filter/sort/projection may not reference withheld fields",
			map[string]any{"fields": stripped}), true
	}
	return stripped, nil, false
}

// policyDatasets lists a policy table's dataset names, sorted;
// unstripped=true keeps only datasets with no strip list (the set
// aggregate may touch — its output is caller-shaped, so withheld
// fields cannot be stripped from it).
func policyDatasets(policy map[string][]string, unstripped bool) []string {
	out := make([]string, 0, len(policy))
	for name, stripped := range policy {
		if unstripped && len(stripped) > 0 {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// queryTouchesFields reports whether the request's filter or sort
// references any of the fields (or a subpath of one).
func queryTouchesFields(root *fastjson.Value, fields []string) bool {
	hit := func(path string) bool {
		path = strings.TrimPrefix(path, "-")
		for _, f := range fields {
			if path == f || strings.HasPrefix(path, f+".") {
				return true
			}
		}
		return false
	}
	// scanFilter walks v as a FILTER document. Field paths occur only
	// as filter-document keys — at the top level and inside the
	// logical combinators' sub-filters. Everything else (a comparison
	// operator's value, an implicit-equality object literal) is DATA:
	// a literal that merely contains a key named like a withheld field
	// must not be rejected.
	var scanFilter func(v *fastjson.Value) bool
	scanFilter = func(v *fastjson.Value) bool {
		if v == nil || v.Type() != fastjson.TypeObject {
			return false
		}
		obj, _ := v.Object()
		found := false
		obj.Visit(func(k []byte, sub *fastjson.Value) {
			if found {
				return
			}
			switch key := string(k); key {
			case "$and", "$or", "$nor":
				for _, e := range sub.GetArray() {
					if scanFilter(e) {
						found = true
						return
					}
				}
			default:
				found = !strings.HasPrefix(key, "$") && hit(key)
			}
		})
		return found
	}
	if scanFilter(root.Get("filter")) {
		return true
	}
	for _, e := range root.GetArray("sort") {
		if e.Type() == fastjson.TypeString && hit(string(e.GetStringBytes())) {
			return true
		}
	}
	// A projection's keys are field paths too. Naming a withheld field
	// there would be stripped anyway; refusing keeps one answer for
	// every way of referencing one, instead of a silent hole in the
	// records where filter and sort would have said why.
	if proj := root.Get("projection"); proj != nil && proj.Type() == fastjson.TypeObject {
		obj, _ := proj.Object()
		found := false
		obj.Visit(func(k []byte, _ *fastjson.Value) {
			if hit(string(k)) {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

// unsupportedError maps the SDK's restricted-handle refusal. Shared by
// the space / op / bundle error mappers.
func unsupportedError(c echo.Context, err error, details map[string]any) (error, bool) {
	if errors.Is(err, space.ErrUnsupported) || errors.Is(err, space.ErrIsTechSpace) {
		return writeError(c, http.StatusMethodNotAllowed, codeSpaceUnsupported,
			"not available on the tech space", details), true
	}
	return nil, false
}
