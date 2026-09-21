package config

import "testing"

// An unconfigured binary joins the production network, so it gets the
// production relays with it — and the tristate then resolves the layer
// on, without anyone writing a config file.
func TestGlobalP2PDefaultsAppliedWithEmbeddedNodeconf(t *testing.T) {
	cfg := Defaults()
	ApplyGlobalP2PDefaults(&cfg)
	if got := cfg.P2P.Global.RelayUrls; len(got) != len(ProdRelayUrls) {
		t.Fatalf("relayUrls = %v, want the production relays", got)
	}
	for i, want := range ProdRelayUrls {
		if cfg.P2P.Global.RelayUrls[i] != want {
			t.Errorf("relayUrls[%d] = %q, want %q", i, cfg.P2P.Global.RelayUrls[i], want)
		}
	}
	if got := cfg.P2P.Global.PkarrRelayUrls; len(got) != 1 || got[0] != ProdPkarrRelayUrls[0] {
		t.Errorf("pkarrRelayUrls = %v, want %v", got, ProdPkarrRelayUrls)
	}
	if !cfg.P2P.Global.IsEnabled() {
		t.Error("global p2p should be on once the packaged relays are in")
	}
}

// The defaults are copied, so mutating one config's list can never
// reach the package-level constants and every later boot.
func TestGlobalP2PDefaultsCopyTheSlices(t *testing.T) {
	cfg := Defaults()
	ApplyGlobalP2PDefaults(&cfg)
	cfg.P2P.Global.RelayUrls[0] = "https://evil.example"
	cfg.P2P.Global.PkarrRelayUrls[0] = "https://evil.example"

	other := Defaults()
	ApplyGlobalP2PDefaults(&other)
	if other.P2P.Global.RelayUrls[0] != ProdRelayUrls[0] {
		t.Errorf("relay default mutated to %q", other.P2P.Global.RelayUrls[0])
	}
	if other.P2P.Global.PkarrRelayUrls[0] != ProdPkarrRelayUrls[0] {
		t.Errorf("pkarr default mutated to %q", other.P2P.Global.PkarrRelayUrls[0])
	}
}

// The pairing rule: a config that names a network is not on production,
// so it must not reach Anytype's relays. This is what keeps every test
// — they all name a nodeconf — off them, and what leaves a self-hosted
// network to name its own.
func TestGlobalP2PDefaultsSkippedWhenNetworkConfigured(t *testing.T) {
	for name, n := range map[string]Network{
		"path":   {NodeconfPath: "staging.yml"},
		"inline": {Nodeconf: "networkId: local"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Network = n
			ApplyGlobalP2PDefaults(&cfg)
			if len(cfg.P2P.Global.RelayUrls) > 0 || len(cfg.P2P.Global.PkarrRelayUrls) > 0 {
				t.Errorf("relays defaulted in on a configured network: %v / %v",
					cfg.P2P.Global.RelayUrls, cfg.P2P.Global.PkarrRelayUrls)
			}
			if cfg.P2P.Global.IsEnabled() {
				t.Error("global p2p must stay off without relays")
			}
		})
	}
}

// Caller-supplied relays are never touched, and naming only one of the
// two lists is a deliberate setup — a device that wants space-level
// discovery without the account record, or the other way round — not
// something to silently complete from production values.
func TestGlobalP2PDefaultsNeverOverrideCaller(t *testing.T) {
	t.Run("relays only", func(t *testing.T) {
		cfg := Defaults()
		cfg.P2P.Global.RelayUrls = []string{"https://relay.example"}
		ApplyGlobalP2PDefaults(&cfg)
		if len(cfg.P2P.Global.RelayUrls) != 1 || cfg.P2P.Global.RelayUrls[0] != "https://relay.example" {
			t.Errorf("relayUrls overwritten to %v", cfg.P2P.Global.RelayUrls)
		}
		if len(cfg.P2P.Global.PkarrRelayUrls) > 0 {
			t.Errorf("pkarr filled from production next to a caller relay: %v", cfg.P2P.Global.PkarrRelayUrls)
		}
	})
	t.Run("pkarr only", func(t *testing.T) {
		cfg := Defaults()
		cfg.P2P.Global.PkarrRelayUrls = []string{"https://dns.example"}
		ApplyGlobalP2PDefaults(&cfg)
		if len(cfg.P2P.Global.RelayUrls) > 0 {
			t.Errorf("relays filled from production next to a caller pkarr relay: %v", cfg.P2P.Global.RelayUrls)
		}
	})
}

// An explicit opt-out still wins over the packaged default: no iroh
// endpoint, no relay session, nothing on the wire.
func TestGlobalP2PRespectsExplicitDisable(t *testing.T) {
	cfg := Defaults()
	off := false
	cfg.P2P.Global.Enabled = &off
	ApplyGlobalP2PDefaults(&cfg)
	if len(cfg.P2P.Global.RelayUrls) == 0 {
		t.Fatal("relays should still be filled; enablement is a separate switch")
	}
	if cfg.P2P.Global.IsEnabled() {
		t.Error("p2p.global.enabled:false must beat the packaged default")
	}
}

// And an explicit opt-in is honored on a custom network, where nothing
// is defaulted: the operator naming relays is the whole configuration.
func TestGlobalP2PExplicitEnableOnCustomNetwork(t *testing.T) {
	cfg := Defaults()
	cfg.Network = Network{NodeconfPath: "staging.yml"}
	on := true
	cfg.P2P.Global.Enabled = &on
	cfg.P2P.Global.RelayUrls = []string{"https://relay.example"}
	ApplyGlobalP2PDefaults(&cfg)
	if !cfg.P2P.Global.IsEnabled() {
		t.Error("an explicit enable must hold on a custom network")
	}
	if len(cfg.P2P.Global.PkarrRelayUrls) > 0 {
		t.Errorf("pkarr defaulted in on a custom network: %v", cfg.P2P.Global.PkarrRelayUrls)
	}
}
