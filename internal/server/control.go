package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
)

// mintControlToken returns a fresh random control token (32 bytes,
// hex). Minted once per managed server that was not handed one by its
// host; announced on stdout right after LISTENING, so only the
// spawning parent — the owner of that pipe — can read it.
func mintControlToken() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

// requireControl enforces the managed-mode control token on the
// request: a managed server accepts the call only with the token its
// host holds, otherwise it answers 403 control.forbidden and false is
// returned (the response is already written). On a standalone server
// there is no token to check — the caller applies its own by-mode
// refusal — so this returns true.
func (d *deps) requireControl(c echo.Context) bool {
	if !d.cfg.Managed() {
		return true
	}
	got := c.Request().Header.Get(api.ControlTokenHeader)
	if d.controlToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(d.controlToken)) == 1 {
		return true
	}
	_ = writeError(c, http.StatusForbidden, "control.forbidden",
		"managed server: this operation requires the control token ("+api.ControlTokenHeader+")", nil)
	return false
}

// capabilities reports the lifecycle operations this server accepts.
// Every bit follows the mode: a managed host owns the server's
// session and lifetime, a standalone server refuses all three.
func (d *deps) capabilities() api.AuthCapabilities {
	managed := d.cfg.Managed()
	return api.AuthCapabilities{
		Deauthorize:   managed,
		SwitchAccount: managed,
		Shutdown:      managed,
	}
}
