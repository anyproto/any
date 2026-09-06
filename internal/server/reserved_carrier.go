package server

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
)

// reservedCarrierType reports whether typeId is a user type whose
// part declares a module reserved to the server (handler.Module.Reserved
// — `chat`): the catalog's install root, carried by no other object.
// The SDK's local write pre-flight enforces the rule on every path that
// adds a type (handler.ErrValidationReservedCarrier); this read lets a
// handler refuse before a side effect. Unknown and registered types
// report false — the SDK answers for them.
func reservedCarrierType(ctx context.Context, sp space.Space, typeId string) bool {
	info, err := sp.Types().Get(ctx, typeId)
	if err != nil || info.BuiltIn {
		return false
	}
	parts, err := sp.Types().Parts(ctx, typeId)
	if err != nil {
		return false
	}
	for _, p := range parts {
		for _, ds := range p.Datasets {
			if reservedModule(ds.Module) {
				return true
			}
		}
	}
	return false
}

// reservedCarrierError is the 400 for attaching a reserved carrier
// type to an object other than its root.
func reservedCarrierError(c echo.Context, spaceId, typeId string) error {
	return writeError(c, http.StatusBadRequest, "type.reserved_carrier",
		"this type declares a module reserved to the server and is carried only by its own root — "+
			"the space's catalog install, not a new object (POST /v1/catalog/{usecaseId}/setup returns it)",
		map[string]any{"typeId": typeId, "spaceId": spaceId})
}
