package server

import (
	"net/http"
	"sync"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// localDiscoverySwitch is the host's answer to "may this device announce
// and browse on the local network?", held for the process lifetime.
//
// It exists because the server cannot find this out for itself. The
// SDK's mDNS driver is raw-socket Go, and macOS enforces local-network
// privacy with a packet filter that drops the packets and reports
// nothing, so a refused permission looks exactly like an empty LAN. And
// the prompt itself fires on the first multicast send, so a host that
// owns the permission flow must keep discovery off until the user has
// answered. Only the host (Apple's own NWBrowser does report the
// refusal) knows either, and it says so here.
//
// The same switch serves the user-facing setting: "off" and "the OS
// said no" both mean "do not scan"; the client knows which one it is.
type localDiscoverySwitch struct {
	mu      sync.Mutex
	stated  bool
	enabled bool
}

// set records the host's answer. Whether it differs from the last one
// is not its concern: the SDK's switch is idempotent, and gating on a
// change here would leave a statement made during an engine boot
// unapplied and unrepeatable.
func (s *localDiscoverySwitch) set(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stated, s.enabled = true, enabled
}

// get is the host's answer, and whether one was ever given.
func (s *localDiscoverySwitch) get() (enabled, stated bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled, s.stated
}

// localDiscoveryState is the switch as the caller sees it: the live
// engine's reading when one is up (entered through the gate, like the
// guard does), otherwise what the next boot will start with.
func (d *deps) localDiscoveryState() bool {
	if d.ready.Load() && d.gate.enter() {
		defer d.gate.leave()
		return d.sdk.LocalDiscoveryEnabled()
	}
	if enabled, stated := d.localDiscovery.get(); stated {
		return enabled
	}
	return d.cfg.LocalDiscoveryEnabled()
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
// the LAN before the host allows it. With an engine live the switch is
// applied at once, within the gate.
//
//	@Summary	Switch local-network discovery (mDNS) on or off
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.LocalDiscoveryRequest	true	"Desired state"
//	@Success	200		{object}	api.LocalDiscoveryResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Router		/local-discovery [put]
func (d *deps) localDiscoverySet(c echo.Context) error {
	req, ok := bindBodyStrict[api.LocalDiscoveryRequest](c, "")
	if !ok {
		return nil
	}
	if req.Enabled == nil {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "enabled required", nil)
	}
	d.localDiscovery.set(*req.Enabled)
	d.applyLocalDiscovery()
	return c.JSON(http.StatusOK, api.LocalDiscoveryResponse{Enabled: *req.Enabled})
}

// applyLocalDiscovery forwards the host's statement, if any, to the
// live engine, entered through the gate like the guard does. A no-op
// without an engine, and idempotent: the SDK ignores a restatement.
// Called on every PUT and once an engine is published, which is what
// catches a statement made while that engine was booting (after
// bootConfig read the switch, before ready flipped).
func (d *deps) applyLocalDiscovery() {
	enabled, stated := d.localDiscovery.get()
	if !stated || !d.ready.Load() || !d.gate.enter() {
		return
	}
	defer d.gate.leave()
	d.sdk.SetLocalDiscoveryEnabled(enabled)
}
