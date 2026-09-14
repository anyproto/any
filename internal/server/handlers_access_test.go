package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anyproto/any-sync/util/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/anyproto/any/internal/config"
)

// fakeInviteService verifies the signed payload the way any-invite does
// and answers with whatever the test scripted for the code.
func fakeInviteService(t *testing.T, answers map[string]struct {
	status int
	body   string
}) (*httptest.Server, *[]accessPayload) {
	t.Helper()
	var seen []accessPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/redeem", r.URL.Path)
		var req accessRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		raw, err := base64.StdEncoding.DecodeString(req.Payload)
		require.NoError(t, err)
		sig, err := base64.StdEncoding.DecodeString(req.Signature)
		require.NoError(t, err)
		var p accessPayload
		require.NoError(t, json.Unmarshal(raw, &p))
		pub, err := crypto.DecodeAccountAddress(p.OwnerAnyId)
		require.NoError(t, err)
		ok, err := pub.Verify(raw, sig)
		require.NoError(t, err)
		require.True(t, ok, "signature must verify against ownerAnyId")
		seen = append(seen, p)
		a, found := answers[p.Code]
		if !found {
			a.status, a.body = http.StatusNotFound, `{"error":{"code":"code.not_found","message":"unknown code"}}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestRedeemAccessCode(t *testing.T) {
	key, _, err := crypto.GenerateRandomEd25519KeyPair()
	require.NoError(t, err)
	now := time.Unix(1_757_400_000, 0)
	srv, seen := fakeInviteService(t, map[string]struct {
		status int
		body   string
	}{
		"GOOD-CODE": {http.StatusAccepted, `{"status":"accepted","redemptionId":"r1"}`},
		"AGAIN":     {http.StatusOK, `{"status":"already_redeemed","redemptionId":"r1"}`},
		"SPENT":     {http.StatusConflict, `{"error":{"code":"code.exhausted","message":"code cannot be redeemed"}}`},
		"BROKEN":    {http.StatusInternalServerError, `oops`},
	})
	ctx := context.Background()

	res, aerr := redeemAccessCode(ctx, srv.Client(), srv.URL+"/", key, "GOOD-CODE", now)
	require.Nil(t, aerr)
	assert.Equal(t, "accepted", res.Status)
	assert.Equal(t, "r1", res.RedemptionId)
	require.Len(t, *seen, 1)
	assert.Equal(t, accessPayload{Purpose: accessPurpose, OwnerAnyId: key.GetPublic().Account(), Code: "GOOD-CODE", Ts: now.Unix()}, (*seen)[0])

	res, aerr = redeemAccessCode(ctx, srv.Client(), srv.URL, key, "AGAIN", now)
	require.Nil(t, aerr)
	assert.Equal(t, "already_redeemed", res.Status)

	_, aerr = redeemAccessCode(ctx, srv.Client(), srv.URL, key, "SPENT", now)
	require.NotNil(t, aerr)
	assert.Equal(t, http.StatusConflict, aerr.status)
	assert.Equal(t, "access.code_unusable", aerr.code)
	assert.Equal(t, "code.exhausted", aerr.details["code"])

	_, aerr = redeemAccessCode(ctx, srv.Client(), srv.URL, key, "NOPE", now)
	require.NotNil(t, aerr)
	assert.Equal(t, http.StatusNotFound, aerr.status)
	assert.Equal(t, "access.code_not_found", aerr.code)

	_, aerr = redeemAccessCode(ctx, srv.Client(), srv.URL, key, "BROKEN", now)
	require.NotNil(t, aerr)
	assert.Equal(t, http.StatusBadGateway, aerr.status)
	assert.Equal(t, "access.unavailable", aerr.code)

	_, aerr = redeemAccessCode(ctx, srv.Client(), "http://127.0.0.1:1", key, "GOOD-CODE", now)
	require.NotNil(t, aerr)
	assert.Equal(t, "access.unavailable", aerr.code)
}

func TestAccountRedeemAccessCode_Disabled(t *testing.T) {
	d, teardown := newTestDeps(t)
	defer teardown()
	e := buildEcho(d)
	rec := doJSON(t, e, http.MethodPost, "/v1/account/access-code", `{"code":"ABCD"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "access.disabled")

	rec = doJSON(t, e, http.MethodPost, "/v1/account/access-code", `{"code":"  "}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAccountRedeemAccessCode_Relays(t *testing.T) {
	srv, seen := fakeInviteService(t, map[string]struct {
		status int
		body   string
	}{
		"ABCD-EFGH": {http.StatusAccepted, `{"status":"accepted","redemptionId":"r9"}`},
	})
	d, teardown := newTestDepsCfg(t, func(cfg *config.Config) { cfg.Access.RedeemUrl = srv.URL })
	defer teardown()
	e := buildEcho(d)
	rec := doJSON(t, e, http.MethodPost, "/v1/account/access-code", `{"code":" abcd-efgh "}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"accepted"`)
	require.Len(t, *seen, 1)
	assert.Equal(t, "ABCD-EFGH", (*seen)[0].Code)
	assert.Equal(t, d.account, (*seen)[0].OwnerAnyId)
}
