package config

// The production push node — the anytype-push-server deployment `any`
// shares with heart (docs/20-push.md). A direct out-of-band peer, so it
// is config, not nodeconf: these constants are the packaged default the
// way nodeconf-prod.yml is, keeping the two in step.
//
// yamux-only, hence the explicit scheme: the node speaks no QUIC, and a
// bare host:port would be dialed on both.
const (
	ProdPushPeerId = "12D3KooWMATrdteJNq2YvYhtq3RDeWxq6RVXDAr36MsGd5RJzXDn"
	ProdPushAddr   = "yamux://anytype-push-server.anytype.io:443"
)

// ApplyPushDefaults fills in the production push node when the config
// names none AND the network is the embedded production default.
//
// The pairing is the point: the push node is not part of the nodeconf,
// so nothing else stops a server on staging or local infra from pushing
// through the production node. Tying the default to the network keeps
// "unconfigured binary" (production, push on) and "explicitly pointed
// somewhere else" (host supplies its own push node, or gets none)
// apart, and keeps every test — which always names a nodeconf — off the
// production push server.
//
// Only a config that names NEITHER field is filled: a half-configured
// push node stays the caller's to finish. `push.enabled: false` still
// wins afterwards, through the tristate.
func ApplyPushDefaults(cfg *Config) {
	if cfg.Push.PeerId != "" || len(cfg.Push.Addrs) > 0 {
		return
	}
	if !UsesEmbeddedNodeconf(cfg.Network) {
		return
	}
	cfg.Push.PeerId = ProdPushPeerId
	cfg.Push.Addrs = []string{ProdPushAddr}
}

// UsesEmbeddedNodeconf reports whether this network config resolves to
// the packaged production nodeconf — i.e. nothing was configured, so
// LoadNodeconf returns the embedded default.
func UsesEmbeddedNodeconf(n Network) bool {
	return n.Nodeconf == "" && n.NodeconfPath == ""
}
