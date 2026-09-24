package embedded

import "testing"

// A host that supplied no nodeconf is on the production network, so it
// gets the global layer — and must be able to say no, since this path
// reads neither a config file nor ANY_* env, and publishing a ticket
// into every space's records is one-way.
func TestAssembleConfigGlobalP2P(t *testing.T) {
	on, off := true, false

	t.Run("production default", func(t *testing.T) {
		cfg := assembleConfig(Options{DataDir: "/tmp/x"})
		if !cfg.P2P.GlobalEnabled() {
			t.Error("an unconfigured embedded host is on production; the layer should run")
		}
	})
	t.Run("host opts out", func(t *testing.T) {
		cfg := assembleConfig(Options{DataDir: "/tmp/x", GlobalP2PEnabled: &off})
		if cfg.P2P.GlobalEnabled() {
			t.Error("a host must be able to refuse the endpoint")
		}
	})
	t.Run("named network gets nothing by default", func(t *testing.T) {
		cfg := assembleConfig(Options{DataDir: "/tmp/x", NodeconfYAML: "networkId: local"})
		if cfg.P2P.GlobalEnabled() {
			t.Error("naming a network must not reach Anytype's relays")
		}
	})
	t.Run("host opts in on a named network without relays", func(t *testing.T) {
		cfg := assembleConfig(Options{DataDir: "/tmp/x", NodeconfYAML: "networkId: local", GlobalP2PEnabled: &on})
		if !cfg.P2P.GlobalEnabled() {
			t.Error("an explicit opt-in must be honored")
		}
		if len(cfg.P2P.Global.RelayUrls) != 0 {
			t.Error("...but no relays may be defaulted in for it")
		}
	})
}
