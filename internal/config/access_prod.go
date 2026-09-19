package config

// ProdRedeemUrl is the production any-invite deployment, packaged the
// way the push node is: an out-of-band service the nodeconf does not
// name, so the binary carries it as config.
const ProdRedeemUrl = "https://prod-any-invite.anytype.io"

// ApplyAccessDefaults fills in the production invite service when the
// config names none AND the network is the embedded production default.
//
// Same pairing as ApplyPushDefaults: the invite service grants limits
// on the production pp-node, so a server on staging or local infra must
// not send its users there. A named nodeconf gets an invite service
// only by naming one, and tests — which always name a nodeconf — stay
// off production.
func ApplyAccessDefaults(cfg *Config) {
	if cfg.Access.RedeemUrl != "" {
		return
	}
	if !UsesEmbeddedNodeconf(cfg.Network) {
		return
	}
	cfg.Access.RedeemUrl = ProdRedeemUrl
}
