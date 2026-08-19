package config

import "testing"

// An unconfigured binary joins the production network, so it gets the
// production push node with it.
func TestPushDefaultsAppliedWithEmbeddedNodeconf(t *testing.T) {
	cfg := Defaults()
	ApplyPushDefaults(&cfg)
	if cfg.Push.PeerId != ProdPushPeerId {
		t.Errorf("peerId = %q, want the production node", cfg.Push.PeerId)
	}
	if len(cfg.Push.Addrs) != 1 || cfg.Push.Addrs[0] != ProdPushAddr {
		t.Errorf("addrs = %v, want [%s]", cfg.Push.Addrs, ProdPushAddr)
	}
	if !cfg.Push.Active() {
		t.Error("push should be active on the packaged default")
	}
}

// The pairing rule: a config that names a network is not on production,
// so it must not inherit the production push node. This is what keeps
// tests (which always name a nodeconf) off the production push server.
func TestPushDefaultsSkippedWhenNetworkConfigured(t *testing.T) {
	for name, n := range map[string]Network{
		"path":   {NodeconfPath: "/etc/any/staging.yml"},
		"inline": {Nodeconf: "networkId: local"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Network = n
			ApplyPushDefaults(&cfg)
			if cfg.Push.PeerId != "" || len(cfg.Push.Addrs) > 0 {
				t.Errorf("push defaulted to %q/%v on a configured network", cfg.Push.PeerId, cfg.Push.Addrs)
			}
			if cfg.Push.Active() {
				t.Error("push must stay off when the network is not production")
			}
		})
	}
}

// A caller-supplied push node is never overwritten, and a half-supplied
// one is left alone rather than silently completed with production
// values that would not match it.
func TestPushDefaultsNeverOverrideCaller(t *testing.T) {
	cfg := Defaults()
	cfg.Push.PeerId = "12D3KooWOther"
	ApplyPushDefaults(&cfg)
	if cfg.Push.PeerId != "12D3KooWOther" {
		t.Errorf("peerId overwritten to %q", cfg.Push.PeerId)
	}
	if len(cfg.Push.Addrs) > 0 {
		t.Errorf("addrs filled from production for a caller-named peer: %v", cfg.Push.Addrs)
	}
}

// An explicit opt-out still wins over the packaged default.
func TestPushDefaultsRespectExplicitDisable(t *testing.T) {
	cfg := Defaults()
	off := false
	cfg.Push.Enabled = &off
	ApplyPushDefaults(&cfg)
	if cfg.Push.Active() {
		t.Error("push.enabled:false must beat the packaged default")
	}
}
