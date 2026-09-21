package server

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// localDiscoveryState is the switch as the caller sees it: the live
// engine's reading when one is up (entered through the gate, like the
// guard does), otherwise what the next boot will start with. False
// whenever p2p.enabled is off: the switch cannot turn on what never
// runs.
func (d *deps) localDiscoveryState() bool {
	if d.ready.Load() && d.gate.enter() {
		defer d.gate.leave()
		return d.sdk.LocalDiscoveryEnabled()
	}
	if p2p := d.cfg.P2P.Enabled; p2p != nil && !*p2p {
		return false
	}
	return d.bootConfig().LocalDiscoveryEnabled()
}

// localDiscoveryGet handles GET /v1/local-discovery.
//
//	@Summary	Whether this device announces and browses on the local network
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	api.LocalDiscoveryResponse
//	@Router		/local-discovery [get]
func (d *deps) localDiscoveryGet(c echo.Context) error {
	return c.JSON(http.StatusOK, api.LocalDiscoveryResponse{Enabled: d.localDiscoveryState()})
}

// localDiscoverySet handles PUT /v1/local-discovery: the host switching
// mDNS announce and browse on or off.
//
// Works before the first POST /v1/auth (the route is exempt from the
// guard): the answer is folded into the SDK config of every engine
// boot, so discovery starts in the stated state and nothing is sent on
// the LAN before the host allows it. With an engine live, or booting,
// the switch is applied at once. Host-owned, so a managed server asks
// for the control token like the other host operations.
//
//	@Summary	Switch local-network discovery (mDNS) on or off
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalDiscoveryRequest	true	"Desired state"
//	@Success	200		{object}	api.LocalDiscoveryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	403		{object}	api.ErrorEnvelope
//	@Router		/local-discovery [put]
func (d *deps) localDiscoverySet(c echo.Context) error {
	if !d.requireControl(c) {
		return nil
	}
	req, ok := bindBodyStrict[api.LocalDiscoveryRequest](c, "")
	if !ok {
		return nil
	}
	if req.Enabled == nil {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "enabled required", nil)
	}
	enabled := *req.Enabled
	d.localDiscovery.Store(&enabled)
	d.applyLocalDiscovery()
	return c.JSON(http.StatusOK, api.LocalDiscoveryResponse{Enabled: d.localDiscoveryState()})
}

// applyLocalDiscovery forwards the host's statement, if any, to the
// engine: the live one, entered through the gate like the guard does,
// else the one booting right now (bootingSDK), so a statement is
// honoured within seconds even during a long restore. A no-op with
// neither, and idempotent: the SDK ignores a restatement. Called on
// every PUT and once an engine is published, which closes the window
// between the booting pointer being cleared and ready flipping.
func (d *deps) applyLocalDiscovery() {
	enabled := d.localDiscovery.Load()
	if enabled == nil {
		return
	}
	if d.ready.Load() && d.gate.enter() {
		d.sdk.SetLocalDiscoveryEnabled(*enabled)
		d.gate.leave()
		return
	}
	if sdk := d.bootingSDK.Load(); sdk != nil {
		sdk.SetLocalDiscoveryEnabled(*enabled)
	}
}
