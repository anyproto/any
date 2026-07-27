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
//	@Success	200	{object}	api.AuthResponse
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Router		/auth [post]
func (d *deps) authorize(c echo.Context) error {
	req, ok := bindBody[api.AuthRequest](c)
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
