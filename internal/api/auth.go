package api

// AuthAccount is one locally-available account in AuthStatusResponse.
type AuthAccount struct {
	// Id is the StrKey account address ("A…"). Empty for a default
	// root wallet whose id can't be derived without a passkey.
	Id string `json:"id,omitempty"`
	// Default marks the legacy flat-layout wallet at the data-dir root.
	Default bool `json:"default,omitempty"`
}

// AuthStatusResponse is the GET /v1/auth reply.
type AuthStatusResponse struct {
	Authorized bool          `json:"authorized"`
	AccountId  string        `json:"accountId,omitempty"`
	Accounts   []AuthAccount `json:"accounts"`
}

// AuthRequest is the POST /v1/auth body. Mnemonic and AccountId are
// mutually exclusive; with neither set a fresh account is generated.
type AuthRequest struct {
	// Mnemonic restores (or first-creates) the account derived from
	// this BIP-39 phrase. The device key is always freshly generated.
	Mnemonic string `json:"mnemonic,omitempty"`
	// AccountId selects an account that already has a local wallet.
	AccountId string `json:"accountId,omitempty"`
	// Index is the account derivation index for Mnemonic. Default 0.
	Index uint32 `json:"index,omitempty"`
}

// AuthResponse is the POST /v1/auth reply.
type AuthResponse struct {
	AccountId string `json:"accountId"`
	// Created is true when a new wallet file was written (fresh
	// generation or first restore on this machine).
	Created bool `json:"created"`
	// Mnemonic is returned exactly once: when the server generated a
	// fresh account (no mnemonic/accountId in the request). The caller
	// must surface it to the user for backup.
	Mnemonic string `json:"mnemonic,omitempty"`
}
