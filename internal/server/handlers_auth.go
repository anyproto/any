package server

import (
	"errors"
	"net/http"
	"os"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"

	"github.com/anyproto/any-sync-sdk/auth"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

var authLog = logger.NewNamed("auth")

func registerAuthRoutes(g *echo.Group, d *deps) {
	g.GET("/auth", d.authStatus)
	g.POST("/auth", d.authorize)
	// Deliberately NOT in the unauthorized-guard exemption list
	// (routes.go): on a server with no account there is nothing to
	// verify against, and the guard's 401 auth.required is the right
	// answer — the handler never has to re-check readiness.
	g.POST("/auth/verify", d.authVerify)
}

// authStatus handles GET /v1/auth.
//
//	@Summary	Authorization state + locally available accounts
//	@Tags		auth
//	@Produce	json
//	@Success	200	{object}	api.AuthStatusResponse
//	@Router		/auth [get]
func (d *deps) authStatus(c echo.Context) error {
	resp := api.AuthStatusResponse{
		Authorized: d.ready.Load(),
		AccountId:  d.accountID(),
		Accounts:   []api.AuthAccount{},
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
	if d.ready.Load() && d.eng != nil && d.cfg.Auth.WalletPath == "" {
		// The engine booted from the root exactly when no per-account
		// dir matches its account id.
		if _, err := os.Stat(config.WalletPath(d.cfg, config.AccountDir(d.root, d.account))); err != nil {
			return d.account
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
// account and boot the engine for it.
//
//	@Summary	Authorize: generate, restore (mnemonic) or select an account
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.AuthRequest	false	"Auth mode: empty body generates, mnemonic restores, accountId selects"
//	@Success	200	{object}	api.AuthResponse
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Router		/auth [post]
func (d *deps) authorize(c echo.Context) error {
	req, ok := bindBodyStrict[api.AuthRequest](c, "modes: {} generates a new account, {\"mnemonic\": …[, \"index\": N]} restores, {\"accountId\": …} selects a local wallet")
	if !ok {
		return nil
	}
	if req.Mnemonic != "" && req.AccountId != "" {
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"mnemonic and accountId are mutually exclusive", nil)
	}
	if req.Index != 0 && req.Mnemonic == "" {
		// index is the derivation index for a restored mnemonic; it is
		// meaningless when selecting an existing account (the index is
		// baked into its wallet) or generating a fresh one (always 0).
		return writeError(c, http.StatusBadRequest, "request.invalid_field",
			"index applies only to mnemonic", nil)
	}
	if d.ready.Load() {
		return writeError(c, http.StatusConflict, "auth.already_authorized",
			"server already runs account "+d.accountID(), nil)
	}

	var (
		identity  *Identity
		seed      walletSeed // mnemonic + index passed through to wallet creation
		generated string     // fresh mnemonic to return once
	)
	switch {
	case req.Mnemonic != "":
		id, err := auth.AccountId(req.Mnemonic, req.Index)
		if err != nil {
			return writeError(c, http.StatusBadRequest, "auth.bad_mnemonic", "invalid mnemonic", nil)
		}
		identity, seed = d.identityForAccount(c, id), walletSeed{mnemonic: req.Mnemonic, index: req.Index}

	case req.AccountId != "":
		identity = d.identityForAccount(c, req.AccountId)
		if _, err := os.Stat(identity.WalletPath); err != nil {
			return writeError(c, http.StatusNotFound, "auth.account_not_found",
				"no local wallet for account "+req.AccountId, nil)
		}

	default:
		m, err := auth.GenerateMnemonic()
		if err != nil {
			authLog.Error("generate mnemonic", zap.Error(err))
			return writeError(c, http.StatusInternalServerError, "internal", "generate mnemonic", nil)
		}
		id, err := auth.AccountId(m, 0)
		if err != nil {
			authLog.Error("derive account id", zap.Error(err))
			return writeError(c, http.StatusInternalServerError, "internal", "derive account id", nil)
		}
		dir := config.AccountDir(d.root, id)
		identity = &Identity{Account: id, Dir: dir, WalletPath: config.WalletPath(config.Config{}, dir)}
		seed, generated = walletSeed{mnemonic: m}, m
	}

	_, statErr := os.Stat(identity.WalletPath)
	created := os.IsNotExist(statErr)

	eng, err := d.bootAccount(identity, seed)
	if err != nil {
		return d.authBootError(c, err)
	}
	return c.JSON(http.StatusOK, api.AuthResponse{
		AccountId: eng.account,
		Created:   created,
		Mnemonic:  generated,
	})
}

// authVerify handles POST /v1/auth/verify.
//
// Answers one question for an ALREADY-AUTHORIZED server: is this phrase
// the running account's? POST /v1/auth cannot answer it — it 409s on a
// ready server before ever reading the mnemonic (see authorize) — so a
// client that wants to confirm a phrase (to store it in the device's
// secure enclave, say) has no way to do so today, and storing an
// unverified phrase would bind a possibly-foreign key to this account.
//
// The check is pure: auth.AccountId derives the account address from the
// phrase without touching disk or booting anything, and the result is
// compared against the id this server already runs. No engine state is
// read or changed, and the phrase is neither stored nor logged (the HTTP
// log records method/path/status only — never the body).
//
// SECURITY. The reply is one bit on purpose: the id derived from a
// non-matching phrase is NOT echoed, so this cannot be used to map an
// arbitrary phrase to its account. Brute force is not a concern (a
// 12-word BIP-39 phrase is 128 bits), but the endpoint does confirm
// "this phrase belongs to this machine's account" to anyone who can
// reach the API — which is why it must stay a JSON-body POST: as a
// simple request (GET, or a text/plain body) any web page could reach
// it past CORS preflight. The loopback-only listen remains the trust
// boundary, exactly as for the rest of /v1.
//
//	@Summary	Check a recovery phrase against the running account
//	@Tags		auth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		api.AuthVerifyRequest	true	"phrase to check"
//	@Success	200		{object}	api.AuthVerifyResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	401		{object}	api.ErrorEnvelope
//	@Router		/auth/verify [post]
func (d *deps) authVerify(c echo.Context) error {
	req, ok := bindBodyStrict[api.AuthVerifyRequest](c, "body: {\"mnemonic\": …[, \"index\": N]}")
	if !ok {
		return nil
	}
	if req.Mnemonic == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "mnemonic required", nil)
	}
	id, err := auth.AccountId(req.Mnemonic, req.Index)
	if err != nil {
		return writeError(c, http.StatusBadRequest, "auth.bad_mnemonic", "invalid mnemonic", nil)
	}
	// Plain comparison: both sides are public account addresses (the
	// running one is already served by GET /v1/auth), so there is no
	// secret to leak through timing.
	return c.JSON(http.StatusOK, api.AuthVerifyResponse{Matches: id == d.accountID()})
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
	var locked *ErrLocked
	switch {
	case errors.Is(err, errAlreadyAuthorized):
		return writeError(c, http.StatusConflict, "auth.already_authorized",
			"server already runs account "+d.accountID(), nil)
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
	}
	authLog.Error("boot account", zap.Error(err))
	return writeError(c, http.StatusInternalServerError, "internal", "account boot failed", nil)
}
