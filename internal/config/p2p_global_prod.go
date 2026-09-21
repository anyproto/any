package config

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

	// ProdPkarrRelayUrls is n0's public iroh-dns-server, used until
	// Anytype hosts its own. Records reach it already signed and
	// encrypted, so it learns an opaque key and an opaque blob, but it
	// is a third party in the discovery path and carries no SLA.
	ProdPkarrRelayUrls = []string{
		"https://dns.iroh.link",
	}
)

// ApplyGlobalP2PDefaults fills in the production relays when the config
// names none AND the network is the embedded production default.
//
// Same pairing as ApplyPushDefaults: a device on staging, local infra
// or a self-hosted network reaches Anytype's relays only by naming
// them, and tests — which always name a nodeconf — never do. Without
// the pairing every test run would hold a relay session.
//
// Only a config that names NEITHER relay list is filled: a partly
// configured layer stays the caller's to finish. `p2p.global.enabled:
// false` still wins afterwards, through the tristate.
func ApplyGlobalP2PDefaults(cfg *Config) {
	if len(cfg.P2P.Global.RelayUrls) > 0 || len(cfg.P2P.Global.PkarrRelayUrls) > 0 {
		return
	}
	if !UsesEmbeddedNodeconf(cfg.Network) {
		return
	}
	cfg.P2P.Global.RelayUrls = append([]string(nil), ProdRelayUrls...)
	cfg.P2P.Global.PkarrRelayUrls = append([]string(nil), ProdPkarrRelayUrls...)
}
