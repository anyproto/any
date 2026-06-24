package server

import (
	"context"
	"net/http"
	"sync/atomic"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// registerIdentitiesRoutes wires the account-global identities directory.
// Account-scoped (no :spaceId), so it sits outside the space group like
// sync-status/subscribe. The static `/subscribe` segment is registered
// before the `:identity` wildcard so it isn't swallowed by the matcher.
func registerIdentitiesRoutes(g *echo.Group, d *deps) {
	g.GET("/identities", d.identitiesList)
	g.GET("/identities/subscribe", d.identitiesSubscribe)
	g.GET("/identities/:identity", d.identityGet)
}

// identitiesList handles GET /v1/identities.
//
//	@Summary	List known identities (account-global directory)
//	@Tags		identities
//	@Produce	json
//	@Success	200	{object}	api.IdentitiesListResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/identities [get]
func (d *deps) identitiesList(c echo.Context) error {
	infos, err := d.sdk.Identities().List(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	out := make([]api.IdentityInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, identityInfoToAPI(info))
	}
	return c.JSON(http.StatusOK, api.IdentitiesListResponse{Identities: out})
}

// identityGet handles GET /v1/identities/:identity.
//
//	@Summary	Get a known identity by account address
//	@Tags		identities
//	@Produce	json
//	@Param		identity	path		string	true	"Account identity"
//	@Success	200			{object}	api.IdentityInfo
//	@Failure	400			{object}	api.ErrorEnvelope
//	@Failure	404			{object}	api.ErrorEnvelope
//	@Failure	500			{object}	api.ErrorEnvelope
//	@Router		/identities/{identity} [get]
func (d *deps) identityGet(c echo.Context) error {
	identity := c.Param("identity")
	if identity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "identity required", nil)
	}
	info, ok, err := d.sdk.Identities().Get(c.Request().Context(), identity)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"identity": identity})
	}
	if !ok {
		return writeError(c, http.StatusNotFound, "identity.not_found", "identity not in the directory", nil)
	}
	return c.JSON(http.StatusOK, identityInfoToAPI(info))
}

// identitiesSubscribe handles GET /v1/identities/subscribe.
//
// Account-wide SSE stream of directory changes: callback→channel bridge
// (the SDK delivers IdentityListEvent batches), same frame envelope and
// reason set as the sync-status streams.
//
//	@Summary	Subscribe to identities directory changes (SSE)
//	@Tags		identities
//	@Produce	text/event-stream
//	@Success	200
//	@Router		/identities/subscribe [get]
func (d *deps) identitiesSubscribe(c echo.Context) error {
	events := make(chan space.IdentityListEvent, statusForwardBuffer)
	var dropped atomic.Uint64
	cancelSub := d.sdk.Identities().Subscribe(func(evt space.IdentityListEvent) {
		select {
		case events <- evt:
		default:
			dropped.Add(1)
		}
	})
	defer cancelSub()

	return d.streamStatusSSE(c, &dropped, func(ctx context.Context, emit func(string, any) error) error {
		for {
			select {
			case evt := <-events:
				if err := emit("identities", identityEventToAPI(evt)); err != nil {
					return err
				}
			case <-ctx.Done():
				return nil
			}
		}
	})
}

// identityInfoToAPI maps the SDK directory row to the wire shape. The
// SDK already strips the synced symKey (the decryption secret) — it
// never reaches this layer.
func identityInfoToAPI(info space.IdentityInfo) api.IdentityInfo {
	return api.IdentityInfo{
		Identity:    info.Identity,
		Name:        info.Name,
		Description: info.Description,
		IconCID:     info.IconCID,
		SpaceIds:    info.SpaceIds,
	}
}

func identityEventToAPI(evt space.IdentityListEvent) api.IdentityEventPayload {
	out := api.IdentityEventPayload{Removed: evt.Removed}
	for _, info := range evt.Added {
		out.Added = append(out.Added, identityInfoToAPI(info))
	}
	for _, info := range evt.Updated {
		out.Updated = append(out.Updated, identityInfoToAPI(info))
	}
	return out
}
