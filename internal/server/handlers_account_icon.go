package server

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync-sdk/space"

	"github.com/anyproto/any/internal/api"
)

// The account avatar: a files-v2 object in the owner's tech space that
// contacts fetch by content address and decrypt with the key sealed in
// the profile. Writes go through Account.SetIcon / DeleteIcon; the own
// read through Account.OpenIcon, a contact's through
// Identities.OpenIcon.

// accountIconPut handles PUT /v1/account/icon.
//
// The raw request body is the image. Its type is read from the bytes
// alone — the declared Content-Type is not trusted — and must be a
// raster image (imageType), because contacts' servers serve what is
// stored here on their own API origin. The SDK caps the size.
//
//	@Summary	Set the account avatar (raw body upload)
//	@Tags		account
//	@Accept		octet-stream
//	@Produce	json
//	@Param		name	query		string	false	"User-facing file name"
//	@Success	200	{object}	api.AccountMetadata
//	@Failure	400	{object}	api.ErrorEnvelope
//	@Failure	413	{object}	api.ErrorEnvelope
//	@Failure	415	{object}	api.ErrorEnvelope
//	@Failure	500	{object}	api.ErrorEnvelope
//	@Router		/account/icon [put]
func (d *deps) accountIconPut(c echo.Context) error {
	name := c.QueryParam("name")
	body, head := peekHead(c.Request().Body)
	if len(head) == 0 {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "the request body is the image; it is empty", nil)
	}
	mimeType, ok := imageType(head)
	if !ok {
		return writeError(c, http.StatusUnsupportedMediaType, api.ErrAccountIconNotImage,
			"the avatar must be a PNG, JPEG, GIF, WebP, AVIF, HEIC, HEIF, BMP, ICO, TIFF or JXL image", map[string]any{"detected": mimeType})
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
	r, err := d.sdk.Account().OpenIcon(c.Request().Context())
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

// iconError maps an OpenIcon failure: ErrNotFound and an avatar the
// SDK refuses as oversized to the caller's 404 code, the fetch states
// like any file download.
func iconError(c echo.Context, err error, notFoundCode, notFoundMsg string, details map[string]any) error {
	switch {
	case errors.Is(err, space.ErrNotFound):
		return writeError(c, http.StatusNotFound, notFoundCode, notFoundMsg, details)
	case errors.Is(err, space.ErrIconTooLarge):
		return writeError(c, http.StatusNotFound, notFoundCode, "the avatar exceeds the size cap and is not served", details)
	}
	return fileError(c, err, details)
}

// serveIcon writes an avatar as an image response. The bytes were
// chosen by another account, so the response never lets a browser
// treat them as anything active: the type comes from the content's
// own signature (a non-image serves as octet-stream), sniffing is
// disabled, and a CSP forbids every load. Range/206 come from
// ServeContent over the seekable reader.
func serveIcon(c echo.Context, r space.FileReader) error {
	defer r.Close()
	head := make([]byte, sniffLimit)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return iconReadError(c, err)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return iconReadError(c, err)
	}
	contentType, ok := imageType(head[:n])
	if !ok {
		contentType = "application/octet-stream"
	}
	// Past Open the bytes exist; a read that fails here, before the
	// response starts, is the fetch of a block that is not local yet —
	// the same retry-later state as an Open that could not start. A
	// fetch that fails once ServeContent is streaming cuts the body
	// like any file download; the client resumes with Range.
	h := c.Response().Header()
	h.Set(echo.HeaderContentType, contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline")
	http.ServeContent(c.Response(), c.Request(), "", time.Time{}, r)
	return nil
}

// iconReadError maps a failure while reading an opened avatar: the
// caller's own cancellation as such, anything else — a peer's read
// budget included — as retry-later.
func iconReadError(c echo.Context, err error) error {
	if c.Request().Context().Err() != nil {
		return fileError(c, err, nil)
	}
	return writeError(c, http.StatusConflict, api.ErrFileNotAvailable, "avatar bytes not available yet; retry later", nil)
}

// imageType classifies head by its signature and reports whether it is
// one of the raster formats an avatar may be. The list is closed: SVG
// is a document that can carry script, and the sniffer's other image
// types (bitmap sources in C, CAD drawings, editor projects) are files
// no client renders.
func imageType(head []byte) (string, bool) {
	mt := bareType(mimetype.Detect(head).String())
	return mt, avatarTypes[mt]
}

// avatarTypes are the media types accepted as an avatar and served as
// an image, by content signature; the animated variants the sniffer
// names separately are the same formats.
var avatarTypes = map[string]bool{
	"image/png":           true,
	"image/apng":          true,
	"image/jpeg":          true,
	"image/gif":           true,
	"image/webp":          true,
	"image/avif":          true,
	"image/heic":          true,
	"image/heic-sequence": true,
	"image/heif":          true,
	"image/heif-sequence": true,
	"image/bmp":           true,
	"image/x-icon":        true,
	"image/tiff":          true,
	"image/jxl":           true,
}

func accountMetadataToAPI(meta space.AccountMetadata) api.AccountMetadata {
	return api.AccountMetadata{
		Name:        meta.Name,
		Description: meta.Description,
		IconCID:     meta.IconCID,
	}
}
