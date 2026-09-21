package server

import (
	"context"
	"sync/atomic"

	sdkp2p "github.com/anyproto/any-sync-sdk/p2p"
)

// localNetworkGate is the host's answer to "may this device use the
// local network?", sitting behind the SDK's possibility probe.
//
// It exists because the SDK cannot find out on its own. Its mDNS driver
// is pure Go over raw multicast sockets, and macOS enforces
// local-network privacy with a Network Extension packet filter that
// drops the packets and reports nothing — so a refused permission is
// indistinguishable from an empty LAN, and the device announces into a
// void for the whole session. The signal exists only in Apple's own
// stack, which means only the desktop shell can observe it. It tells us
// through PUT /v1/p2p/local-network, and we tell the SDK through here.
//
// The same lever serves the user-facing switch: "off" and "the OS said
// no" both mean "do not scan", and the client already knows which one
// it is. The server deliberately does not try to distinguish them.
//
// Zero value is open: absent a host that says otherwise, this changes
// nothing about how the SDK behaves.
type localNetworkGate struct {
	closed atomic.Bool
}

// probe is installed through p2p.SetPossibilityProbe. Unknown is the SDK's
// "nothing to add" — it then applies its own interface check, which is
// the half of the answer this gate has no opinion about.
func (g *localNetworkGate) probe(context.Context, int) sdkp2p.Possibility {
	if g.closed.Load() {
		return sdkp2p.PossibilityRestricted
	}
	return sdkp2p.PossibilityUnknown
}

// set records the host's answer and reports whether it changed.
func (g *localNetworkGate) set(enabled bool) bool {
	return g.closed.Swap(!enabled) == enabled
}

// enabled is the current answer, for the debug snapshot.
func (g *localNetworkGate) enabled() bool { return !g.closed.Load() }

// localNetwork is the per-process gate, created on first use so every
// deps construction path gets one with no explicit wiring (it has no
// engine or SDK dependency).
func (d *deps) localNetwork() *localNetworkGate {
	d.localNetworkOnce.Do(func() { d.localNetworkGate = &localNetworkGate{} })
	return d.localNetworkGate
}

// installLocalNetworkProbe points the SDK's possibility probe at this
// process's gate. Called before every engine boot: the probe is an SDK
// process global while the gate lives on deps, so the host's answer
// survives logout, an account switch and any other engine restart.
func (d *deps) installLocalNetworkProbe() {
	sdkp2p.SetPossibilityProbe(d.localNetwork().probe)
}
