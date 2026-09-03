package api

// ControlTokenHeader carries the managed-mode control token. A managed
// server accepts POST/DELETE /v1/auth and POST /v1/shutdown only with
// the token its spawning host holds; a standalone server refuses those
// operations regardless (docs/03-api.md § Auth).
const ControlTokenHeader = "X-Any-Control-Token"

// AuthAccount is one locally-available account in AuthStatusResponse.
type AuthAccount struct {
	// Id is the StrKey account address ("A…"). Empty for a default
	// root wallet whose id can't be derived without a passkey.
	Id string `json:"id,omitempty"`
	// Default marks the legacy flat-layout wallet at the data-dir root.
	Default bool `json:"default,omitempty"`
}

// AuthCapabilities are the lifecycle operations this server accepts.
// Clients branch on these bits, never on the mode string, so a future
// mode does not break them.
type AuthCapabilities struct {
	// Deauthorize: DELETE /v1/auth tears the account down in place.
	Deauthorize bool `json:"deauthorize"`
	// SwitchAccount: POST /v1/auth with replace:true switches accounts
	// in place.
	SwitchAccount bool `json:"switchAccount"`
	// Shutdown: POST /v1/shutdown stops the server.
	Shutdown bool `json:"shutdown"`
}

// AuthStatusResponse is the GET /v1/auth reply.
type AuthStatusResponse struct {
	Authorized bool   `json:"authorized"`
	AccountId  string `json:"accountId,omitempty"`
	// Mode is the server's ownership mode, fixed at launch.
	Mode string `json:"mode" enums:"standalone,managed"`
	// Capabilities lists which lifecycle operations this server
	// accepts; every bit is false on a standalone server.
	Capabilities AuthCapabilities `json:"capabilities"`
	// Accounts are the wallets on disk (standalone only — a managed
	// server holds no keys and reports an empty list; the client owns
	// the account list there).
	Accounts []AuthAccount `json:"accounts"`
}

// AuthRequest is the POST /v1/auth body. Mnemonic and AccountId are
// mutually exclusive; with neither set a fresh account is generated.
type AuthRequest struct {
	// Mnemonic restores (or first-creates) the account derived from
	// this BIP-39 phrase.
	Mnemonic string `json:"mnemonic,omitempty"`
	// AccountId selects an account that already has a local wallet
	// (standalone only).
	AccountId string `json:"accountId,omitempty"`
	// Index is the account derivation index for Mnemonic. Omitted
	// means 1, the `any` default; pass 0 explicitly to restore an
	// anytype-derived (or pre-index-1 any) account.
	Index *uint32 `json:"index,omitempty"`
	// Replace switches a managed server from its current account to
	// the one this request names, tearing the current engine down
	// first. Without it a different account is refused. Never implied.
	Replace bool `json:"replace,omitempty"`
}

// AuthResponse is the POST /v1/auth reply.
type AuthResponse struct {
	AccountId string `json:"accountId"`
	// Created is true when this account had no local state before this
	// call (fresh generation or first restore on this device).
	Created bool `json:"created"`
	// Mnemonic is returned exactly once: when the server generated a
	// fresh account (no mnemonic/accountId in the request). The caller
	// must surface it to the user for backup.
	Mnemonic string `json:"mnemonic,omitempty"`
	// AlreadyAuthorized reports that the request named the account the
	// server already runs: nothing was booted. With a mnemonic this
	// confirms the phrase derives to the running account; with an
	// accountId it confirms only that the id matches.
	AlreadyAuthorized bool `json:"alreadyAuthorized,omitempty"`
}
