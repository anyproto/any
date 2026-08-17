package server

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// DevicesDataset is the tech-space system dataset holding the
// account's device registry: one row per device (peer), row id =
// peerId. Like the space list it lives on the tech-space index object
// and is system-owned — reads go through the endpoints below, writes
// only through the restricted self-row surface (never the generic
// modify path). See docs/21-devices.md.
const DevicesDataset = "devices"

var devicesQueryFields = jsonFieldNames(reflect.TypeFor[api.DevicesQueryRequest]())

// registerDevicesRoutes wires the account-global device registry.
// Account-scoped (no :spaceId), so it sits outside the space group
// like sync-status/subscribe. Static segments (me, activate, query)
// are registered before the :peerId wildcard so they aren't swallowed
// by the matcher.
func registerDevicesRoutes(g *echo.Group, d *deps) {
	g.GET("/devices", d.devicesList)
	g.POST("/devices/query", d.devicesQuery)
	g.POST("/devices/query/subscribe", d.devicesQuerySubscribe)
	g.PUT("/devices/me", d.deviceUpdateMe)
	g.POST("/devices/activate", d.deviceActivate)
	g.DELETE("/devices/:peerId", d.deviceDelete)
}

// devicesList handles GET /v1/devices.
//
// The mapped convenience over the `devices` dataset — every registered
// device plus the per-app active winner resolved by the SDK's
// canonical space.ActiveDevice rule. UI and agent runtimes consume
// `active` instead of reimplementing the rule; the raw rows (and live
// updates) are available through POST /v1/devices/query[/subscribe].
//
//	@Summary	List the account's devices and per-app active winners
//	@Tags		devices
//	@Produce	json
//	@Success	200	{object}	api.DevicesListResponse
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/devices [get]
func (d *deps) devicesList(c echo.Context) error {
	devices, err := d.sdk.Spaces().ListDevices(c.Request().Context())
	if err != nil {
		return sdkOpError(c, err, nil)
	}
	out := make([]api.DeviceInfo, 0, len(devices))
	for _, dev := range devices {
		out = append(out, deviceToAPI(dev))
	}
	return c.JSON(http.StatusOK, api.DevicesListResponse{
		Devices: out,
		Active:  activeDevicesMap(devices),
		Self:    d.sdk.PeerId(),
	})
}

// deviceUpdateMe handles PUT /v1/devices/me — the restricted self-row
// write (Spaces.SetDevice). The row identity is never taken from the
// caller: the SDK resolves its own peerId, so this endpoint can only
// ever touch THIS device's row. os/version are stamped by the server
// on every boot; the body carries only the caller-owned fields. A
// null value under `apps` uninstalls that slug.
//
//	@Summary	Update this device's registry row (name, installed apps)
//	@Tags		devices
//	@Accept		json
//	@Param		body	body	api.DeviceUpdateRequest	true	"Fields to set"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/devices/me [put]
func (d *deps) deviceUpdateMe(c echo.Context) error {
	req, ok := bindBodyStrict[api.DeviceUpdateRequest](c, "")
	if !ok {
		return nil
	}
	if req.Name == "" && len(req.Apps) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"at least one of name or apps is required", nil)
	}
	err := d.sdk.Spaces().SetDevice(c.Request().Context(), space.DeviceUpsert{
		Name: req.Name,
		Apps: req.Apps,
	})
	if err != nil {
		return deviceError(c, err, nil)
	}
	return c.NoContent(http.StatusNoContent)
}

// deviceActivate handles POST /v1/devices/activate — claim the active
// role for one app slug on THIS device (Spaces.ClaimActive). The claim
// is writer-supplied data (`{seq: max visible + 1, at: now}`), never a
// CRDT version id — versionIds are peer-local and cannot arbitrate
// across devices. The SDK also self-heals the `apps.<slug>` installed
// marker so a claim never dangles. Winners are read back from
// GET /v1/devices `active`.
//
//	@Summary	Claim the active role for an app on this device
//	@Tags		devices
//	@Accept		json
//	@Param		body	body	api.DeviceActivateRequest	true	"App slug"
//	@Success	204
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/devices/activate [post]
func (d *deps) deviceActivate(c echo.Context) error {
	req, ok := bindBodyStrict[api.DeviceActivateRequest](c, "")
	if !ok {
		return nil
	}
	if req.App == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "app required", nil)
	}
	if err := d.sdk.Spaces().ClaimActive(c.Request().Context(), req.App); err != nil {
		return deviceError(c, err, map[string]any{"app": req.App})
	}
	return c.NoContent(http.StatusNoContent)
}

// deviceDelete handles DELETE /v1/devices/:peerId — prune a device's
// row (Spaces.DeleteDevice), the "device doesn't exist" signal the
// election reacts to. CRDT record tombstones are sticky: a deleted
// peerId can NEVER re-register — a pruned device that comes back stays
// unlisted until it re-derives fresh peer keys (a new `any init`).
// Prune dead devices, not resting ones.
//
//	@Summary	Remove a device from the registry (permanent for that peerId)
//	@Tags		devices
//	@Param		peerId	path	string	true	"Device peer id"
//	@Success	204
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/devices/{peerId} [delete]
func (d *deps) deviceDelete(c echo.Context) error {
	peerId := c.Param("peerId")
	if err := d.sdk.Spaces().DeleteDevice(c.Request().Context(), peerId); err != nil {
		return deviceError(c, err, map[string]any{"peerId": peerId})
	}
	return c.NoContent(http.StatusNoContent)
}

// devicesQuery handles POST /v1/devices/query.
//
// The windowed-query counterpart to GET /v1/devices: a raw snapshot
// over the tech-space `devices` dataset through the generic
// Service.Query primitive — same body surface as the space-list query
// minus the dataset override (fixed to `devices`). Records are the raw
// rows; the resolved active map is only on the mapped GET.
//
//	@Summary	Query the account's device registry (windowed)
//	@Tags		devices
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.DevicesQueryRequest	false	"Query params"
//	@Success	200		{object}	api.QueryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	500		{object}	api.ErrorEnvelope
//	@Router		/devices/query [post]
func (d *deps) devicesQuery(c echo.Context) error {
	q, opts, errResp, done := d.buildDevicesQuery(c)
	if done {
		return errResp
	}
	res, err := q.Snapshot(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"dataset": DevicesDataset})
	}
	return writeQueryResponse(c, res, opts.IncludeTotal)
}

// devicesQuerySubscribe handles POST /v1/devices/query/subscribe.
//
// Windowed live view over the device registry — the SSE counterpart to
// devicesQuery, same frame set as every /query/subscribe (ready →
// snapshot → changes → closed). This is the stream runtimes watch to
// observe active-claim movement (stand down / take over) and devices
// appearing or leaving.
//
//	@Summary	Subscribe to the account's device registry (SSE)
//	@Tags		devices
//	@Accept		json
//	@Produce	text/event-stream
//	@Param		body	body	api.DevicesQueryRequest	false	"Query params"
//	@Success	200
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/devices/query/subscribe [post]
func (d *deps) devicesQuerySubscribe(c echo.Context) error {
	q, opts, errResp, done := d.buildDevicesQuery(c)
	if done {
		return errResp
	}
	res, err := q.Subscribe(c.Request().Context(), opts)
	if err != nil {
		return sdkOpError(c, err, map[string]any{"dataset": DevicesDataset})
	}
	return d.streamQuerySubscribe(c, res, opts.IncludeTotal)
}

// buildDevicesQuery assembles the chained Query + QueryOpts for the
// devices endpoints — the space-list builder minus the dataset
// override: objectId is fixed to the tech-space index object, dataset
// to `devices`. The body is optional (an empty body is a full
// snapshot).
func (d *deps) buildDevicesQuery(c echo.Context) (space.Query, space.QueryOpts, error, bool) {
	body, err := readBody(c)
	if err != nil {
		return nil, space.QueryOpts{}, writeError(c, http.StatusBadRequest, "request.bad_json", "unreadable body", nil), true
	}
	parser := getFastjsonParser()
	defer putFastjsonParser(parser)
	var root *fastjson.Value
	if len(body) > 0 {
		root, err = parser.ParseBytes(body)
		if err != nil {
			return nil, space.QueryOpts{}, writeError(c, http.StatusBadRequest, "request.bad_json", "invalid JSON body", nil), true
		}
	}
	if errResp, done := checkUnknownFields(c, root, "", devicesQueryFields...); done {
		return nil, space.QueryOpts{}, errResp, true
	}
	if errResp, done := checkFilter(c, root); done {
		return nil, space.QueryOpts{}, errResp, true
	}
	svc := d.sdk.Spaces()
	q, opts := applyQueryParams(root, svc.Query(svc.SpaceIndexObjectId(), DevicesDataset))
	return q, opts, nil, false
}

// deviceToAPI maps the SDK registry row to the wire shape.
func deviceToAPI(dev space.Device) api.DeviceInfo {
	info := api.DeviceInfo{
		PeerId:  dev.PeerId,
		Name:    dev.Name,
		OS:      dev.OS,
		Version: dev.Version,
		Apps:    dev.Apps,
	}
	if len(dev.ActiveClaims) > 0 {
		info.ActiveClaims = make(map[string]api.DeviceActiveClaim, len(dev.ActiveClaims))
		for slug, claim := range dev.ActiveClaims {
			info.ActiveClaims[slug] = api.DeviceActiveClaim{Seq: claim.Seq, At: claim.At}
		}
	}
	return info
}

// activeDevicesMap resolves every claimed app slug to its winning
// peerId via the SDK's canonical election rule (space.ActiveDevice —
// the ONE implementation; never reimplement it here). A slug whose
// only claims are dangling (app no longer installed anywhere) gets no
// entry.
func activeDevicesMap(devices []space.Device) map[string]string {
	var active map[string]string
	for _, dev := range devices {
		for slug := range dev.ActiveClaims {
			if _, done := active[slug]; done {
				continue
			}
			if winner, ok := space.ActiveDevice(devices, slug); ok {
				if active == nil {
					active = make(map[string]string)
				}
				active[slug] = winner
			}
		}
	}
	return active
}

// deviceError maps the SDK's exported device sentinels onto the wire
// error namespace; anything unrecognized falls through to sdkOpError.
func deviceError(c echo.Context, err error, details map[string]any) error {
	switch {
	case errors.Is(err, space.ErrDeviceBadApp):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"app slugs must be non-empty and dot-free", details)
	case errors.Is(err, space.ErrDeviceBadValue):
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"app info values must be scalars (string, number, bool)", details)
	case errors.Is(err, space.ErrDeviceEmptyUpsert):
		return writeError(c, http.StatusBadRequest, "request.missing_field",
			"nothing to update", details)
	case errors.Is(err, space.ErrDeviceUnknown):
		return writeError(c, http.StatusNotFound, "device.not_found",
			"no device with this peer id", details)
	}
	return sdkOpError(c, err, details)
}
