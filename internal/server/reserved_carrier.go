package server

import (
	"context"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"
)

// reservedCarrierType reports whether typeId is a user type whose
// part declares a module reserved to the server (handler.Module.Reserved
// — `chat`): the catalog's install root, carried by no other object.
// The SDK's local write pre-flight enforces the rule on every path that
// adds a type (handler.ErrValidationReservedCarrier); this read lets a
// handler refuse before a side effect — an object create or a bundle
// root mints its tree before the write the SDK would refuse. The
// verdict is the discovery snapshot (no store read); only an owner
// pays the type read that tells a registered static declaration (not
// a carrier) from a user type. A type the read cannot resolve —
// unknown, or a transient error — reports false and is the SDK's to
// refuse.
func reservedCarrierType(ctx context.Context, sp space.Space, typeId string) bool {
	owner := false
	for _, ds := range sp.Datasets() {
		if reservedModule(ds.Module) && slices.Contains(ds.Owners, typeId) {
			owner = true
			break
		}
	}
	if !owner {
		return false
	}
	info, err := sp.Types().Get(ctx, typeId)
	return err == nil && !info.BuiltIn
}

// reservedCarrierError is the 400 for attaching a reserved carrier
// type to an object other than its root.
func reservedCarrierError(c echo.Context, spaceId, typeId string) error {
	return writeError(c, http.StatusBadRequest, "type.reserved_carrier",
		reservedCarrierMessage, map[string]any{"typeId": typeId, "spaceId": spaceId})
}

// reservedCarrierMessage is the one wording for the refusal, whether
// the server or the SDK decided it.
const reservedCarrierMessage = "this type declares a module reserved to the server and is carried only by its own root — " +
	"the space's catalog install, not another object (POST /v1/catalog/{usecaseId}/setup returns it)"
