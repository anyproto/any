package config

import (
	"errors"
	"fmt"
	"net/url"
)

// The production global-p2p infrastructure: the iroh relays that
// forward encrypted QUIC between two devices that cannot reach each
// other directly, and the pkarr relay that holds each account's
// device-discovery record.
//
// Packaged as config the way the push node and the invite service are:
// neither is named by the nodeconf, so the binary carries the default
// and keeps it in step with nodeconf-prod.yml.
//
// A relay never sees content — it forwards an end-to-end-encrypted
// stream and helps the two sides hole-punch a direct path — and the
// pkarr record is signed and encrypted under keys derived from the
// account identity, so a relay holds opaque bytes it cannot read,
// write, or attribute to an account.
var (
	ProdRelayUrls = []string{
		"https://relay-fr-1.anytype.io",
		"https://relay-de-1.anytype.io",
	}

	// ProdPkarrRelayUrls is the configured pkarr relay: n0's public
	// iroh-dns-server, which n0 sanctions for production use (unlike
	// their public relays) with no uptime guarantee. Records reach it
	// already signed and encrypted, so it learns an opaque key and an
	// opaque blob — but it also learns the publisher's address, which
	// over time correlates the devices of one account by IP. That, not
	// capacity, is the reason to host our own.
	ProdPkarrRelayUrls = []string{
		"https://dns.iroh.link",
	}
)

// ApplyGlobalP2PDefaults fills in the production relays when the
// network is the embedded production default.
//
// Same pairing as ApplyPushDefaults: a device on staging, local infra
// or a self-hosted network reaches Anytype's relays only by naming
// them, and tests — which always name a nodeconf — never do. Without
// the pairing every test run would hold a relay session.
//
// The two lists fill independently, so every combination on the
// production network is coherent: naming your own relays keeps the
// packaged pkarr relay, naming your own pkarr relay keeps the packaged
// relays. Filling only together would make "pkarr relays, nothing
// else" resolve the layer OFF — the SDK cannot run account discovery
// without the transport — which is the opposite of what writing that
// line asks for, and it would fail silently.
//
// `p2p.global.enabled: false` still wins afterwards, through the
// tristate, as does an explicit `p2p.enabled: false` — but only
// against these packaged values, not against relays an operator wrote
// (see GlobalP2P.isEnabled).
func ApplyGlobalP2PDefaults(cfg *Config) {
	if !UsesEmbeddedNodeconf(cfg.Network) {
		return
	}
	if len(cfg.P2P.Global.RelayUrls) == 0 {
		cfg.P2P.Global.RelayUrls = append([]string(nil), ProdRelayUrls...)
		cfg.P2P.Global.relaysDefaulted = true
	}
	if len(cfg.P2P.Global.PkarrRelayUrls) == 0 {
		cfg.P2P.Global.PkarrRelayUrls = append([]string(nil), ProdPkarrRelayUrls...)
	}
}

// validateGlobalP2P refuses a global block the layer cannot run with,
// at config load rather than from inside the SDK at boot: without a
// relay the published ticket would have to carry this device's IP
// addresses into every space's records, so the SDK errors out and
// takes the whole server with it. Catching it here names the key the
// operator actually wrote.
func validateGlobalP2P(cfg *Config) error {
	g := cfg.P2P.Global
	if cfg.P2P.GlobalEnabled() && len(g.RelayUrls) == 0 {
		return errors.New("p2p.global.enabled is true but p2p.global.relayUrls is empty: " +
			"the layer needs a relay, or the published ticket would carry this device's addresses")
	}
	// URLs are checked whether or not the layer is on: the SDK
	// validates them unconditionally, so a malformed one in a disabled
	// block still aborts the boot — from inside the SDK, naming
	// neither the key nor the file the operator wrote it in.
	for _, list := range []struct {
		name     string
		urls     []string
		insecure bool
		flag     string
	}{
		{"relayUrls", g.RelayUrls, g.InsecureRelay, "insecureRelay"},
		{"pkarrRelayUrls", g.PkarrRelayUrls, g.InsecurePkarr, "insecurePkarr"},
	} {
		for _, raw := range list.urls {
			u, err := url.Parse(raw)
			if err != nil || u.Host == "" {
				return fmt.Errorf("p2p.global.%s: %q is not a URL", list.name, raw)
			}
			switch u.Scheme {
			case "https":
			case "http":
				if !list.insecure {
					return fmt.Errorf("p2p.global.%s: %q is plaintext; set p2p.global.%s to allow it",
						list.name, raw, list.flag)
				}
			default:
				return fmt.Errorf("p2p.global.%s: %q must be http(s)", list.name, raw)
			}
		}
	}
	return nil
}
