package config

import "testing"

// An unconfigured binary joins the production network, so it redeems
// through the production invite service.
func TestAccessDefaultsAppliedWithEmbeddedNodeconf(t *testing.T) {
	cfg := Defaults()
	ApplyAccessDefaults(&cfg)
	if cfg.Access.RedeemUrl != ProdRedeemUrl {
		t.Errorf("redeemUrl = %q, want the production service", cfg.Access.RedeemUrl)
	}
}

// A config that names a network is not on production, so it must not
// inherit the production invite service.
func TestAccessDefaultsSkippedWhenNetworkConfigured(t *testing.T) {
	for name, n := range map[string]Network{
		"path":   {NodeconfPath: "/etc/any/staging.yml"},
		"inline": {Nodeconf: "networkId: local"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Network = n
			ApplyAccessDefaults(&cfg)
			if cfg.Access.RedeemUrl != "" {
				t.Errorf("redeemUrl defaulted to %q on a configured network", cfg.Access.RedeemUrl)
			}
		})
	}
}

// A caller-supplied service is never overwritten.
func TestAccessDefaultsNeverOverrideCaller(t *testing.T) {
	cfg := Defaults()
	cfg.Access.RedeemUrl = "https://invite.example.org"
	ApplyAccessDefaults(&cfg)
	if cfg.Access.RedeemUrl != "https://invite.example.org" {
		t.Errorf("redeemUrl overwritten to %q", cfg.Access.RedeemUrl)
	}
}
