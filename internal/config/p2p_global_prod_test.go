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
	if !cfg.P2P.GlobalEnabled() {
		t.Error("global p2p should be on once the packaged relays are in")
	}
}

// The defaults are copied, so mutating one config's list can never
// reach the package-level vars and every later boot. The expectations
// are captured BEFORE the mutation: comparing against the package var
// afterwards would pass even when it was the thing mutated.
func TestGlobalP2PDefaultsCopyTheSlices(t *testing.T) {
	wantRelay, wantPkarr := ProdRelayUrls[0], ProdPkarrRelayUrls[0]

	cfg := Defaults()
	ApplyGlobalP2PDefaults(&cfg)
	cfg.P2P.Global.RelayUrls[0] = "https://evil.example"
	cfg.P2P.Global.PkarrRelayUrls[0] = "https://evil.example"

	if ProdRelayUrls[0] != wantRelay {
		t.Errorf("package relay default mutated to %q", ProdRelayUrls[0])
	}
	if ProdPkarrRelayUrls[0] != wantPkarr {
		t.Errorf("package pkarr default mutated to %q", ProdPkarrRelayUrls[0])
	}
	other := Defaults()
	ApplyGlobalP2PDefaults(&other)
	if other.P2P.Global.RelayUrls[0] != wantRelay {
		t.Errorf("a later boot got %q", other.P2P.Global.RelayUrls[0])
	}
	if other.P2P.Global.PkarrRelayUrls[0] != wantPkarr {
		t.Errorf("a later boot got pkarr %q", other.P2P.Global.PkarrRelayUrls[0])
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
			if cfg.P2P.GlobalEnabled() {
				t.Error("global p2p must stay off without relays")
			}
		})
	}
}

// Caller-supplied relays are never overwritten, and the list the
// caller did NOT name still gets its production default — so naming
// only pkarr relays cannot leave the transport empty and resolve the
// whole layer off, which is the opposite of what writing that line
// asks for.
func TestGlobalP2PDefaultsFillEachListIndependently(t *testing.T) {
	t.Run("relays only", func(t *testing.T) {
		cfg := Defaults()
		cfg.P2P.Global.RelayUrls = []string{"https://relay.example"}
		ApplyGlobalP2PDefaults(&cfg)
		if len(cfg.P2P.Global.RelayUrls) != 1 || cfg.P2P.Global.RelayUrls[0] != "https://relay.example" {
			t.Errorf("relayUrls overwritten to %v", cfg.P2P.Global.RelayUrls)
		}
		if len(cfg.P2P.Global.PkarrRelayUrls) != 1 || cfg.P2P.Global.PkarrRelayUrls[0] != ProdPkarrRelayUrls[0] {
			t.Errorf("pkarrRelayUrls = %v, want the packaged default", cfg.P2P.Global.PkarrRelayUrls)
		}
		if !cfg.P2P.GlobalEnabled() {
			t.Error("layer must run on the caller's relays")
		}
	})
	t.Run("pkarr only", func(t *testing.T) {
		cfg := Defaults()
		cfg.P2P.Global.PkarrRelayUrls = []string{"https://dns.example"}
		ApplyGlobalP2PDefaults(&cfg)
		if len(cfg.P2P.Global.PkarrRelayUrls) != 1 || cfg.P2P.Global.PkarrRelayUrls[0] != "https://dns.example" {
			t.Errorf("pkarrRelayUrls overwritten to %v", cfg.P2P.Global.PkarrRelayUrls)
		}
		if len(cfg.P2P.Global.RelayUrls) == 0 {
			t.Fatal("naming a pkarr relay must not leave the transport unconfigured")
		}
		if !cfg.P2P.GlobalEnabled() {
			t.Error("layer must run: a pkarr relay is useless without the transport")
		}
	})
}

// An explicit LAN opt-out suppresses the derived global default: a
// config whose whole content is "turn peer-to-peer off" must not
// acquire an iroh endpoint and a dialable ticket in every space.
func TestGlobalP2PFollowsAnExplicitLanOptOut(t *testing.T) {
	cfg := Defaults()
	off := false
	cfg.P2P.Enabled = &off
	ApplyGlobalP2PDefaults(&cfg)
	if cfg.P2P.GlobalEnabled() {
		t.Error("p2p.enabled:false must not leave the global layer on by derivation")
	}

	// ...but naming it explicitly still wins: the layers are independent.
	on := true
	cfg.P2P.Global.Enabled = &on
	if !cfg.P2P.GlobalEnabled() {
		t.Error("an explicit global enable must survive p2p.enabled:false")
	}
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
	if cfg.P2P.GlobalEnabled() {
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
	if !cfg.P2P.GlobalEnabled() {
		t.Error("an explicit enable must hold on a custom network")
	}
	if len(cfg.P2P.Global.PkarrRelayUrls) > 0 {
		t.Errorf("pkarr defaulted in on a custom network: %v", cfg.P2P.Global.PkarrRelayUrls)
	}
}

// A global block the layer cannot run with must fail at config load,
// naming the key the operator wrote — not from inside the SDK at boot,
// which aborts the whole server with SDK wording.
func TestValidateGlobalP2P(t *testing.T) {
	custom := Network{NodeconfPath: "staging.yml"}
	on := true

	t.Run("enabled with no relays", func(t *testing.T) {
		cfg := Defaults()
		cfg.Network = custom
		cfg.P2P.Global.Enabled = &on
		if err := validateGlobalP2P(&cfg); err == nil {
			t.Fatal("want an error naming relayUrls")
		}
	})
	t.Run("plaintext relay needs the opt-in", func(t *testing.T) {
		cfg := Defaults()
		cfg.Network = custom
		cfg.P2P.Global.RelayUrls = []string{"http://relay.internal:3340"}
		if err := validateGlobalP2P(&cfg); err == nil {
			t.Fatal("want an error naming insecureRelay")
		}
		cfg.P2P.Global.InsecureRelay = true
		if err := validateGlobalP2P(&cfg); err != nil {
			t.Errorf("insecureRelay must admit it: %v", err)
		}
	})
	t.Run("plaintext pkarr needs its own opt-in", func(t *testing.T) {
		cfg := Defaults()
		cfg.Network = custom
		cfg.P2P.Global.RelayUrls = []string{"https://relay.example"}
		cfg.P2P.Global.PkarrRelayUrls = []string{"http://dns.internal:3341"}
		if err := validateGlobalP2P(&cfg); err == nil {
			t.Fatal("want an error naming insecurePkarr")
		}
		cfg.P2P.Global.InsecurePkarr = true
		if err := validateGlobalP2P(&cfg); err != nil {
			t.Errorf("insecurePkarr must admit it: %v", err)
		}
	})
	t.Run("junk url", func(t *testing.T) {
		cfg := Defaults()
		cfg.Network = custom
		cfg.P2P.Global.RelayUrls = []string{"relay.example:3340"}
		if err := validateGlobalP2P(&cfg); err == nil {
			t.Fatal("a scheme-less relay must be refused, not silently dialed")
		}
	})
	t.Run("a disabled layer is never validated", func(t *testing.T) {
		cfg := Defaults()
		cfg.Network = custom
		off := false
		cfg.P2P.Global.Enabled = &off
		cfg.P2P.Global.RelayUrls = []string{"nonsense"}
		if err := validateGlobalP2P(&cfg); err != nil {
			t.Errorf("an off layer must not fail boot: %v", err)
		}
	})
	t.Run("the packaged production default validates", func(t *testing.T) {
		cfg := Defaults()
		ApplyGlobalP2PDefaults(&cfg)
		if err := validateGlobalP2P(&cfg); err != nil {
			t.Errorf("the shipped default must load: %v", err)
		}
	})
}
