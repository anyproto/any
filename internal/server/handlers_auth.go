package server

import (
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/auth"
	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

var authLog = logger.NewNamed("auth")

func registerAuthRoutes(g *echo.Group, d *deps) {
	g.GET("/auth", d.authStatus)
	g.POST("/auth", d.authorize)
	g.DELETE("/auth", d.deauthorize)
}

// authStatus handles GET /v1/auth.
//
//	@Summary	Authorization state, ownership mode, capabilities and locally available accounts
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	api.AuthStatusResponse
//	@Router		/auth [get]
func (d *deps) authStatus(c echo.Context) error {
	// One gated read decides both fields, so a teardown racing this
	// call never yields authorized:true with an empty account.
	account := d.accountID()
	resp := api.AuthStatusResponse{
		Authorized:   account != "",
		AccountId:    account,
		Mode:         d.cfg.Mode,
		Capabilities: d.capabilities(),
		Accounts:     []api.AuthAccount{},
	}
	if d.cfg.Managed() {
		// A managed server holds no keys, so it cannot enumerate
		// accounts — the client owns the list. Wallets a standalone
		// server left under the same root are deliberately not shown:
		// managed never boots from one.
		return c.JSON(http.StatusOK, resp)
	}

	ids, err := config.ListAccounts(d.root)
	if err != nil {
		return writeError(c, http.StatusInternalServerError, "internal", "list accounts", nil)
	}
	if config.HasRootWallet(d.root) {
		resp.Accounts = append(resp.Accounts, api.AuthAccount{Id: d.rootWalletID(c), Default: true})
	}
	for _, id := range ids {
		resp.Accounts = append(resp.Accounts, api.AuthAccount{Id: id})
	}
	return c.JSON(http.StatusOK, resp)
}

// rootWalletID best-effort derives the default (root) wallet's account
// id: free when it is the booted account, otherwise by opening the
// wallet — which fails silently (empty id) for an encrypted wallet
// without a passkey.
func (d *deps) rootWalletID(c echo.Context) string {
	// Gate-scoped read (accountID), never authMu: this runs on the
	// exempt /v1/auth path while a teardown may hold that lock.
	if account := d.accountID(); account != "" && d.cfg.Auth.WalletPath == "" {
		// The engine booted from the root exactly when no per-account
		// dir matches its account id.
		if _, err := os.Stat(config.WalletPath(d.cfg, config.AccountDir(d.root, account))); err != nil {
			return account
		}
	}
	passkey, err := config.ResolvePasskey(d.cfg, false)
	if err != nil {
		return ""
	}
	provider, _, err := OpenWallet(config.WalletPath(config.Config{}, d.root), passkey, "", 0)
	if err != nil {
		return ""
	}
	id, err := AccountID(c.Request().Context(), provider)
	if err != nil {
		return ""
	}
	return id
}

// authorize handles POST /v1/auth — create, restore or select an
// account and boot the engine for it; on a managed server, also switch
// to another account in place.
//
// The target account is derived from the request BEFORE anything is
// decided, so the same account is always a no-op (200
// alreadyAuthorized — a retry or a duplicate mount never drops every
// stream), and a different one is refused without touching disk:
// 403 auth.not_managed on a standalone server, 409
// auth.account_mismatch on a managed one unless replace is set. A
// refusal never echoes the derived id — that would make the endpoint
// a phrase-to-account oracle. `{}` while authorized is always refused:
// minting an account must never be a side effect of a stale request.
//
//	@Summary	Authorize: generate, restore (mnemonic) or select an account; replace switches (managed)
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		X-Any-Control-Token	header		string			false	"managed servers: the control token"
//	@Param		body				body		api.AuthRequest	false	"Auth mode: empty body generates, mnemonic restores, accountId selects; replace switches on a managed server"
//	@Success	200	{object}	api.AuthResponse
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	403	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Router		/auth [post]
func (d *deps) authorize(c echo.Context) error {
	// Token first: an untokened caller on a managed server learns
	// nothing about the body vocabulary or the mode's refusals.
	if !d.requireControl(c) {
		return nil
	}
	req, ok := bindBodyStrict[api.AuthRequest](c, "modes: {} generates a new account, {\"mnemonic\": …[, \"index\": N]} restores, {\"accountId\": …} selects a local wallet; \"replace\": true switches a managed server to the named account")
	if !ok {
		return nil
	}
	if req.Mnemonic != "" && req.AccountId != "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"mnemonic and accountId are mutually exclusive", nil)
	}
	if req.Index != nil && req.Mnemonic == "" {
		// index is the derivation index for a restored mnemonic; it is
		// meaningless when selecting an existing account (the index is
		// baked into its wallet) or generating a fresh one (always the
		// any default).
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"index applies only to mnemonic", nil)
	}
	if req.Replace && req.Mnemonic == "" && req.AccountId == "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"replace needs a credential — a fresh account is never a replacement", nil)
	}
	if d.cfg.Managed() && req.AccountId != "" {
		// A managed server holds no wallets, so there is nothing to
		// select by id — the host supplies the phrase on every boot.
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"managed server keeps no local wallets — supply the mnemonic", nil)
	}

	// Derive the target first (cheap for every form) so the decision
	// below never has to read a wallet or boot anything.
	idx := auth.DefaultAccountIndex
	if req.Index != nil {
		idx = *req.Index
	}
	target := req.AccountId
	if req.Mnemonic != "" {
		id, err := auth.AccountId(req.Mnemonic, idx)
		if err != nil {
			return writeError(c, http.StatusBadRequest, "auth.bad_mnemonic", "invalid mnemonic", nil)
		}
		target = id
	}

	// Pre-check outside authMu: the cheap, common answers. The boot
	// below re-decides under the lock, so a race with another boot or
	// teardown lands on the same table (see authBootError).
	if current := d.accountID(); current != "" {
		if target == current {
			return c.JSON(http.StatusOK, api.AuthResponse{AccountId: current, AlreadyAuthorized: true})
		}
		if !(d.cfg.Managed() && req.Replace && target != "") {
			return d.refuseOtherAccount(c, target, req.Replace)
		}
		// managed + replace: the switch below tears the current engine
		// down and boots the target.
	}

	var (
		identity  *Identity
		open      credential
		created   bool   // no local state for this account before the call
		generated string // fresh mnemonic to return once
	)
	switch {
	case req.Mnemonic != "":
		identity, open, created = d.credentialFor(c, target, req.Mnemonic, idx)

	case req.AccountId != "":
		identity = d.identityForAccount(c, req.AccountId)
		if _, err := os.Stat(identity.WalletPath); err != nil {
			return writeError(c, http.StatusNotFound, "auth.account_not_found",
				"no local wallet for account "+req.AccountId, nil)
		}
		open = fileCredential(d.cfg, identity.WalletPath, walletSeed{})

	default:
		m, err := auth.GenerateMnemonic()
		if err != nil {
			authLog.Error("generate mnemonic", zap.Error(err))
			return writeError(c, http.StatusInternalServerError, "internal", "generate mnemonic", nil)
		}
		id, err := auth.AccountId(m, auth.DefaultAccountIndex)
		if err != nil {
			authLog.Error("derive account id", zap.Error(err))
			return writeError(c, http.StatusInternalServerError, "internal", "derive account id", nil)
		}
		identity, open, created = d.credentialFor(c, id, m, auth.DefaultAccountIndex)
		generated = m
	}

	var (
		eng *engine
		err error
	)
	if req.Replace && d.cfg.Managed() {
		eng, err = d.switchAccount(identity, open)
	} else {
		eng, err = d.bootAccount(identity, open)
	}
	switch {
	case errors.Is(err, errAlreadyAuthorized):
		// The account came up between the pre-check and the lock
		// (or a same-account replace raced another): a no-op.
		return c.JSON(http.StatusOK, api.AuthResponse{AccountId: eng.account, AlreadyAuthorized: true})
	case errors.Is(err, errAccountMismatch):
		return d.refuseOtherAccount(c, target, req.Replace)
	case err != nil:
		return d.authBootError(c, err)
	}
	return c.JSON(http.StatusOK, api.AuthResponse{
		AccountId: eng.account,
		Created:   created,
		Mnemonic:  generated,
	})
}

// refuseOtherAccount answers a request naming an account other than
// the running one (target "" = a fresh account was asked for). Never
// echoes an account id: the running one is public on GET /v1/auth,
// the derived one would make this endpoint a phrase oracle.
func (d *deps) refuseOtherAccount(c echo.Context, target string, replace bool) error {
	switch {
	case target == "":
		return writeError(c, http.StatusConflict, "auth.already_authorized",
			"server already runs an account — a new one cannot be generated in place", nil)
	case !d.cfg.Managed():
		return writeError(c, http.StatusForbidden, "auth.not_managed",
			"server already runs another account; switching needs a managed server (restart with --account to change it)", nil)
	case !replace:
		return writeError(c, http.StatusConflict, "auth.account_mismatch",
			"server already runs another account; pass replace:true to switch", nil)
	}
	// A replace whose target changed underneath it — the engine that
	// won the race serves yet another account. Retry against the
	// current state.
	return writeError(c, http.StatusConflict, "auth.account_mismatch",
		"server switched to another account meanwhile; re-read GET /v1/auth and retry", nil)
}

// deauthorize handles DELETE /v1/auth — tear the account down in place
// and stay up unauthorized (managed only; a standalone server signs
// out by stopping). Idempotent: nothing to tear down is 204 too.
//
//	@Summary	Deauthorize: tear the account down in place (managed servers)
//	@Tags		auth
//	@Param		X-Any-Control-Token	header	string	false	"managed servers: the control token"
//	@Success	204
//	@Failure	403	{object}	api.ErrorEnvelope
//	@Router		/auth [delete]
func (d *deps) deauthorize(c echo.Context) error {
	if !d.cfg.Managed() {
		return writeError(c, http.StatusForbidden, "auth.not_managed",
			"standalone server: sign out by stopping it", nil)
	}
	if !d.requireControl(c) {
		return nil
	}
	d.teardownEngine(authLog, api.SubscribeClosedDeauthorized)
	return c.NoContent(http.StatusNoContent)
}

// credentialFor resolves where an account derived from a phrase lives
// and how its keys open, by mode. Managed: <root>/<id>/ with the
// account key held in memory for this boot and the device key cached
// in the dir (created = no cache yet, i.e. first login on this
// install). Standalone: the wallet file (created = none on disk yet).
func (d *deps) credentialFor(c echo.Context, id, mnemonic string, index uint32) (*Identity, credential, bool) {
	if d.cfg.Managed() {
		dir := config.AccountDir(d.root, id)
		_, err := os.Stat(deviceKeyPath(dir))
		return &Identity{Account: id, Dir: dir}, managedCredential(dir, mnemonic, index), os.IsNotExist(err)
	}
	identity := d.identityForAccount(c, id)
	_, err := os.Stat(identity.WalletPath)
	return identity, fileCredential(d.cfg, identity.WalletPath, walletSeed{mnemonic: mnemonic, index: index}), os.IsNotExist(err)
}

// identityForAccount places an account id in the root: its per-account
// dir when present (or to be created), else the legacy root wallet —
// boot verifies the root wallet actually is that account.
func (d *deps) identityForAccount(c echo.Context, id string) *Identity {
	dir := config.AccountDir(d.root, id)
	walletPath := config.WalletPath(config.Config{}, dir)
	if _, err := os.Stat(walletPath); err == nil {
		return &Identity{Account: id, Dir: dir, WalletPath: walletPath}
	}
	if config.HasRootWallet(d.root) && d.rootWalletID(c) == id {
		return &Identity{Account: id, Dir: d.root, WalletPath: config.WalletPath(config.Config{}, d.root)}
	}
	return &Identity{Account: id, Dir: dir, WalletPath: walletPath}
}

// authBootError maps engine-boot failures onto the auth error surface.
func (d *deps) authBootError(c echo.Context, err error) error {
	var (
		locked   *ErrLocked
		mismatch *ErrNetworkMismatch
	)
	switch {
	case errors.As(err, &mismatch):
		return writeError(c, http.StatusConflict, "auth.network_mismatch",
			"this account's data belongs to another any-sync network than the server is configured for — start the server with that network's nodeconf, or keep a separate data dir per network",
			map[string]any{"pinned": mismatch.Pinned, "configured": mismatch.Configured})
	case errors.Is(err, errDeviceKeyCorrupt):
		return writeError(c, http.StatusInternalServerError, "auth.device_key_corrupt",
			"this account's cached device key is unreadable — remove device.key from its account dir to mint a new device identity (this device then registers as a new peer)", nil)
	case errors.As(err, &locked):
		return writeError(c, http.StatusConflict, "auth.account_in_use",
			"account is already served by another process", map[string]any{"pid": locked.PID})
	case errors.Is(err, auth.ErrInvalidMnemonic):
		return writeError(c, http.StatusBadRequest, "auth.bad_mnemonic", "invalid mnemonic", nil)
	case errors.Is(err, auth.ErrMnemonicMismatch):
		return writeError(c, http.StatusConflict, "auth.mnemonic_mismatch",
			"existing wallet for this account was created from a different mnemonic/index", nil)
	case errors.Is(err, auth.ErrPasskeyRequired), errors.Is(err, auth.ErrWrongPasskey):
		return writeError(c, http.StatusBadRequest, "auth.passkey_required",
			"wallet is encrypted — provide the passkey via the configured passkey env", nil)
	case errors.Is(err, space.ErrCRDTVersionNewer):
		return writeError(c, http.StatusConflict, "sdk.crdt_version_newer",
			"the account's data was written by a newer version — upgrade this server before opening it",
			crdtVersionDetails(err, nil))
	}
	authLog.Error("boot account", zap.Error(err))
	return writeError(c, http.StatusInternalServerError, "internal", "account boot failed", nil)
}
