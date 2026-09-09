package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/anyproto/any-sync/util/crypto"

	"github.com/anyproto/any/internal/api"
)

// accessPurpose pins the signed payload to the invite service so the
// signature cannot be replayed against another signed-payload API.
const accessPurpose = "any-invite/redeem"

// accessHTTP is the client for the invite service; the timeout bounds a
// handler that would otherwise hang on an unreachable service.
var accessHTTP = &http.Client{Timeout: 15 * time.Second}

type accessPayload struct {
	Purpose    string `json:"purpose"`
	OwnerAnyId string `json:"ownerAnyId"`
	Code       string `json:"code"`
	Ts         int64  `json:"ts"`
}

type accessRequest struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

// accessError is a refusal to relay: an HTTP status and code of our own,
// with the service's code and message in details.
type accessError struct {
	status  int
	code    string
	msg     string
	details map[string]any
}

// accountRedeemAccessCode handles POST /v1/account/access-code.
//
//	@Summary	Redeem an alpha invite code
//	@Tags		account
//	@Accept		json
//	@Produce	json
//	@Param		body	body		api.AccessCodeRequest	true	"The invite code"
//	@Success	200		{object}	api.AccessCodeResponse
//	@Failure	400		{object}	api.ErrorEnvelope
//	@Failure	401		{object}	api.ErrorEnvelope
//	@Failure	404		{object}	api.ErrorEnvelope
//	@Failure	409		{object}	api.ErrorEnvelope
//	@Failure	429		{object}	api.ErrorEnvelope
//	@Failure	502		{object}	api.ErrorEnvelope
//	@Router		/account/access-code [post]
func (d *deps) accountRedeemAccessCode(c echo.Context) error {
	req, ok := bindBodyStrict[api.AccessCodeRequest](c, "")
	if !ok {
		return nil
	}
	code := strings.Join(strings.Fields(strings.ToUpper(req.Code)), "")
	if code == "" {
		return writeError(c, http.StatusBadRequest, "request.missing_field", "code is required", nil)
	}
	if d.cfg.Access.RedeemUrl == "" {
		return writeError(c, http.StatusConflict, "access.disabled",
			"no invite service configured (access.redeemUrl)", nil)
	}
	if d.signKey == nil {
		return writeError(c, http.StatusInternalServerError, "internal", "account key unavailable", nil)
	}
	res, aerr := redeemAccessCode(c.Request().Context(), accessHTTP, d.cfg.Access.RedeemUrl, d.signKey, code, time.Now())
	if aerr != nil {
		return writeError(c, aerr.status, aerr.code, aerr.msg, aerr.details)
	}
	return c.JSON(http.StatusOK, res)
}

// redeemAccessCode signs the payload and posts it to <baseURL>/redeem,
// mapping the service's answer onto the access.* error namespace.
func redeemAccessCode(ctx context.Context, hc *http.Client, baseURL string, key crypto.PrivKey, code string, now time.Time) (api.AccessCodeResponse, *accessError) {
	payload, err := json.Marshal(accessPayload{
		Purpose:    accessPurpose,
		OwnerAnyId: key.GetPublic().Account(),
		Code:       code,
		Ts:         now.Unix(),
	})
	if err != nil {
		return api.AccessCodeResponse{}, &accessError{status: http.StatusInternalServerError, code: "internal", msg: err.Error()}
	}
	sig, err := key.Sign(payload)
	if err != nil {
		return api.AccessCodeResponse{}, &accessError{status: http.StatusInternalServerError, code: "internal", msg: "sign: " + err.Error()}
	}
	body, _ := json.Marshal(accessRequest{
		Payload:   base64.StdEncoding.EncodeToString(payload),
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	url := strings.TrimRight(baseURL, "/") + "/redeem"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return api.AccessCodeResponse{}, &accessError{status: http.StatusBadGateway, code: "access.unavailable", msg: err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(httpReq)
	if err != nil {
		return api.AccessCodeResponse{}, &accessError{status: http.StatusBadGateway, code: "access.unavailable",
			msg: "invite service unreachable", details: map[string]any{"error": err.Error()}}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
		var out api.AccessCodeResponse
		if err := json.Unmarshal(raw, &out); err != nil || out.Status == "" {
			return api.AccessCodeResponse{}, &accessError{status: http.StatusBadGateway, code: "access.unavailable",
				msg: "invite service answered with an unexpected body"}
		}
		return out, nil
	}

	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &envelope)
	details := map[string]any{"code": envelope.Error.Code, "message": envelope.Error.Message}
	switch resp.StatusCode {
	case http.StatusBadRequest:
		return api.AccessCodeResponse{}, &accessError{status: http.StatusBadRequest, code: "access.request_rejected",
			msg: "invite service rejected the request", details: details}
	case http.StatusUnauthorized:
		return api.AccessCodeResponse{}, &accessError{status: http.StatusUnauthorized, code: "access.signature_rejected",
			msg: "invite service could not verify this account's signature", details: details}
	case http.StatusNotFound:
		return api.AccessCodeResponse{}, &accessError{status: http.StatusNotFound, code: "access.code_not_found",
			msg: "unknown invite code", details: details}
	case http.StatusConflict:
		return api.AccessCodeResponse{}, &accessError{status: http.StatusConflict, code: "access.code_unusable",
			msg: "invite code cannot be redeemed", details: details}
	case http.StatusTooManyRequests:
		return api.AccessCodeResponse{}, &accessError{status: http.StatusTooManyRequests, code: "access.rate_limited",
			msg: "invite service is throttling this client; try again later", details: details}
	}
	return api.AccessCodeResponse{}, &accessError{status: http.StatusBadGateway, code: "access.unavailable",
		msg: "invite service answered " + resp.Status, details: details}
}
