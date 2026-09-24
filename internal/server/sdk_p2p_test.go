package server

import (
	"reflect"
	"testing"

	"github.com/anyproto/any/internal/config"
)

// globalP2P resolves the enablement tristate before the SDK sees it,
// so these cases pin what the SDK is actually told to do.
func TestGlobalP2PMapping(t *testing.T) {
	on, off := true, false
	relays := []string{"https://relay.example"}

	for _, tc := range []struct {
		name string
		p2p  config.P2P
		want bool
	}{
		{"relays present, nothing stated", config.P2P{Global: config.GlobalP2P{RelayUrls: relays}}, true},
		{"no relays, nothing stated", config.P2P{}, false},
		{"global off beats relays", config.P2P{Global: config.GlobalP2P{Enabled: &off, RelayUrls: relays}}, false},
		{"global on with no relays is still on (load rejects it)", config.P2P{Global: config.GlobalP2P{Enabled: &on}}, true},
		{"lan off keeps hand-named relays", config.P2P{Enabled: &off, Global: config.GlobalP2P{RelayUrls: relays}}, true},
		{"lan off, global stated on", config.P2P{Enabled: &off, Global: config.GlobalP2P{Enabled: &on, RelayUrls: relays}}, true},
		{"lan off, global stated off", config.P2P{Enabled: &off, Global: config.GlobalP2P{Enabled: &off, RelayUrls: relays}}, false},
		{"lan on stated, relays present", config.P2P{Enabled: &on, Global: config.GlobalP2P{RelayUrls: relays}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := globalP2P(tc.p2p)
			if got.Enabled == nil {
				t.Fatal("Enabled must always be stated to the SDK, never left nil")
			}
			if *got.Enabled != tc.want {
				t.Errorf("Enabled = %v, want %v", *got.Enabled, tc.want)
			}
		})
	}
}

// Every field the SDK reads must cross the mapping — a silently dropped
// budget or insecure flag is invisible until a deployment misbehaves.
func TestGlobalP2PMappingCarriesEveryField(t *testing.T) {
	got := globalP2P(config.P2P{Global: config.GlobalP2P{
		RelayUrls:      []string{"http://relay.internal"},
		PkarrRelayUrls: []string{"http://dns.internal"},
		InsecureRelay:  true,
		InsecurePkarr:  true,
		Port:           41234,
		MaxConnections: 2,
		MaxInbound:     3,
	}})
	if len(got.RelayURLs) != 1 || got.RelayURLs[0] != "http://relay.internal" {
		t.Errorf("RelayURLs = %v", got.RelayURLs)
	}
	if len(got.PkarrRelayURLs) != 1 || got.PkarrRelayURLs[0] != "http://dns.internal" {
		t.Errorf("PkarrRelayURLs = %v", got.PkarrRelayURLs)
	}
	if !got.InsecureRelay || !got.InsecurePkarr {
		t.Error("the insecure opt-ins must reach the SDK, or an http relay aborts boot")
	}
	if got.Port != 41234 || got.MaxConnections != 2 || got.MaxInbound != 3 {
		t.Errorf("budget dropped: port=%d conns=%d inbound=%d", got.Port, got.MaxConnections, got.MaxInbound)
	}
}

// A field added to config.GlobalP2P and forgotten in globalP2P() is
// invisible until a deployment misbehaves, so count them: the mapping
// below has to be revisited whenever the config grows.
func TestGlobalP2PMappingCoversEveryConfigField(t *testing.T) {
	const mapped = 8 // Enabled, RelayUrls, PkarrRelayUrls, InsecureRelay,
	// InsecurePkarr, Port, MaxConnections, MaxInbound — plus the
	// unexported relaysDefaulted marker, which is resolution state and
	// deliberately not passed to the SDK.
	const unexported = 1
	if n := reflect.TypeOf(config.GlobalP2P{}).NumField(); n != mapped+unexported {
		t.Fatalf("config.GlobalP2P has %d fields, the mapping covers %d — "+
			"add the new one to globalP2P() (or to this count if it is not the SDK's)", n, mapped+unexported)
	}
}
