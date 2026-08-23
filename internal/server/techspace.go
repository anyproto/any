package server

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

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

// techIndexAllowedDatasets are the tech index object's datasets
// readable through the per-space query/aggregate on the tech space —
// the same closed set as the account-level space-list query plus the
// bundles registry. `identities` rows carry a synced symKey and stay
// behind GET /v1/identities; the rest are typed-API-only.
var techIndexAllowedDatasets = map[string]struct{}{
	"spaces":  {},
	"profile": {},
	"bundles": {},
}

func (d *deps) isTechSpace(spaceId string) bool {
	return spaceId != "" && d.sdk != nil && spaceId == d.sdk.TechSpaceId()
}

func unsupportedOnTechSpace(c echo.Context, spaceId string) error {
	return writeError(c, http.StatusMethodNotAllowed, codeSpaceUnsupported,
		"not available on the tech space", map[string]any{"spaceId": spaceId})
}

// notOnTechSpace refuses a per-space route for the tech space before
// the handler runs.
func (d *deps) notOnTechSpace(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		if id := c.Param("spaceId"); d.isTechSpace(id) {
			return unsupportedOnTechSpace(c, id)
		}
		return next(c)
	}
}

// techIndexFence applies the index-object dataset allowlist to a
// per-object read on the tech space. Returns the fields to strip from
// `spaces` rows (guest private keys) and whether the request was
// refused. A no-op for regular spaces and for bundle roots.
func (d *deps) techIndexFence(c echo.Context, sp space.Space, objectId, dataset string) (strip []string, errResp error, done bool) {
	if !d.isTechSpace(sp.Id()) || objectId != sp.SpaceIndexObjectId() {
		return nil, nil, false
	}
	if _, ok := techIndexAllowedDatasets[dataset]; !ok {
		return nil, writeError(c, http.StatusBadRequest, "request.invalid_field",
			"dataset must be one of: spaces, profile, bundles on the tech index object (read identities via GET /v1/identities)",
			map[string]any{"objectId": objectId, "dataset": dataset}), true
	}
	if dataset == SpaceListDataset {
		return spaceListStrippedFields, nil, false
	}
	return nil, nil, false
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
