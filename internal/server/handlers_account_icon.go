package server

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// The account avatar: a files-v2 object in the owner's tech space that
// contacts fetch by content address and decrypt with the key sealed in
// the profile. Writes go through Account.SetIcon / DeleteIcon; every
// read — own or a contact's — goes through Identities.OpenIcon.

// accountIconPut handles PUT /v1/account/icon.
//
// The raw request body is the image. The media type is resolved like a
// file attach (Content-Type header, then the content's signature, then
// the name's extension) and must be image/*. The SDK caps the size.
//
//	@Summary	Set the account avatar (raw body upload)
//	@Tags		account
//	@Accept		octet-stream
//	@Produce	json
//	@Param		name	query		string	false	"User-facing file name"
//	@Success	200	{object}	api.AccountMetadata
//	@Failure	413	{object}	api.ErrorEnvelope
//	@Failure	415	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account/icon [put]
func (d *deps) accountIconPut(c echo.Context) error {
	name := c.QueryParam("name")
	var body io.Reader = c.Request().Body
	mimeType := resolveMime(c.Request().Header.Get(echo.HeaderContentType), name, func() []byte {
		var head []byte
		body, head = peekHead(body)
		return head
	})
	if !strings.HasPrefix(mimeType, "image/") {
		return writeError(c, http.StatusUnsupportedMediaType, api.ErrAccountIconNotImage,
			"the avatar must be an image", map[string]any{"mime": mimeType})
	}
	meta, err := d.sdk.Account().SetIcon(c.Request().Context(), body, space.IconOpts{
		Name: ensureNameExt(name, mimeType),
		Mime: mimeType,
	})
	if err != nil {
		if errors.Is(err, space.ErrIconTooLarge) {
			return writeError(c, http.StatusRequestEntityTooLarge, api.ErrAccountIconTooLarge,
				"the avatar exceeds the size cap", map[string]any{"maxBytes": space.MaxIconSize})
		}
		return fileError(c, err, nil)
	}
	return c.JSON(http.StatusOK, accountMetadataToAPI(meta))
}

// accountIconGet handles GET /v1/account/icon.
//
//	@Summary	Download the account avatar (raw bytes, Range supported)
//	@Tags		account
//	@Produce	octet-stream
//	@Success	200
//	@Success	206
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account/icon [get]
func (d *deps) accountIconGet(c echo.Context) error {
	r, err := d.sdk.Identities().OpenIcon(c.Request().Context(), d.sdk.Account().Id())
	if err != nil {
		return iconError(c, err, api.ErrAccountIconNotFound, "no avatar is set", nil)
	}
	return serveIcon(c, r)
}

// accountIconDelete handles DELETE /v1/account/icon.
//
//	@Summary	Remove the account avatar
//	@Tags		account
//	@Success	204
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account/icon [delete]
func (d *deps) accountIconDelete(c echo.Context) error {
	if err := d.sdk.Account().DeleteIcon(c.Request().Context()); err != nil {
		return fileError(c, err, nil)
	}
	return c.NoContent(http.StatusNoContent)
}

// identityIconGet handles GET /v1/identities/:identity/icon.
//
// Serves the avatar the identity's resolved profile names: the bytes
// are fetched by content address through the public read path and
// decrypted with the key from the profile. 404 until the profile
// resolves with an avatar; 409 file.not_available while the bytes
// cannot be fetched (retry later).
//
//	@Summary	Download a known identity's avatar (raw bytes, Range supported)
//	@Tags		identities
//	@Produce	octet-stream
//	@Param		identity	path	string	true	"Account identity"
//	@Success	200
//	@Success	206
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	404	{object}	api.ErrorEnvelope
//	@Failure	409	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/identities/{identity}/icon [get]
func (d *deps) identityIconGet(c echo.Context) error {
	identity := c.Param("identity")
	if identity == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "identity required", nil)
	}
	r, err := d.sdk.Identities().OpenIcon(c.Request().Context(), identity)
	if err != nil {
		return iconError(c, err, api.ErrIdentityIconNotFound,
			"no avatar for this identity (not in the directory, or its profile names none)",
			map[string]any{"identity": identity})
	}
	return serveIcon(c, r)
}

// iconError maps an OpenIcon failure: ErrNotFound to the caller's 404
// code, the fetch states like any file download.
func iconError(c echo.Context, err error, notFoundCode, notFoundMsg string, details map[string]any) error {
	if errors.Is(err, space.ErrNotFound) {
		return writeError(c, http.StatusNotFound, notFoundCode, notFoundMsg, details)
	}
	return fileError(c, err, details)
}

// serveIcon writes an avatar as an image response. The profile carries
// no media type, so it is sniffed from the plaintext; Range/206 come
// from ServeContent over the seekable reader.
func serveIcon(c echo.Context, r space.FileReader) error {
	defer r.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fileError(c, err, nil)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return fileError(c, err, nil)
	}
	c.Response().Header().Set(echo.HeaderContentType, http.DetectContentType(head[:n]))
	http.ServeContent(c.Response(), c.Request(), "", time.Time{}, r)
	return nil
}

func accountMetadataToAPI(meta space.AccountMetadata) api.AccountMetadata {
	return api.AccountMetadata{
		Name:        meta.Name,
		Description: meta.Description,
		IconCID:     meta.IconCID,
	}
}
