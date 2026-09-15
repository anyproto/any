package server

import (
	"net/http"

	"github.com/anyproto/any-sync-sdk/space"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"
)

// identityKeysDataset is the SDK-internal one-to-one key exchange: one
// row per participant on the 1-1's index object, carrying that
// participant's identity metadata symkey. The SDK reads it into the
// identities directory itself; a client resolves the peer through
// GET /v1/identities/:identity, which never carries the key. The raw
// key is non-revocable and outlives the space, so no read route serves
// it — the same rule the tech index's `identities` dataset follows.
const identityKeysDataset = "identityKeys"

// identityKeysReadRefused answers a per-object read that names the
// key-exchange dataset with 400 request.invalid_field. done=false for
// any other dataset.
func identityKeysReadRefused(c echo.Context, objectId, dataset string) (errResp error, done bool) {
	if dataset != identityKeysDataset {
		return nil, false
	}
	return writeError(c, http.StatusBadRequest, "request.invalid_field",
		"dataset "+identityKeysDataset+" is SDK-internal (resolve the peer via GET /v1/identities/:identity)",
		map[string]any{"objectId": objectId, "dataset": dataset}), true
}

// perObjectReadVet is the read-side vet for every per-object query and
// subscribe: the key-exchange refusal on every space, then the tech
// index policy on the tech space.
func (d *deps) perObjectReadVet(c echo.Context, sp space.Space) perObjectVet {
	tech := d.techIndexVet(c, sp)
	return func(root *fastjson.Value, objectId, dataset string) ([]string, error, bool) {
		if errResp, done := identityKeysReadRefused(c, objectId, dataset); done {
			return nil, errResp, true
		}
		if tech == nil {
			return nil, nil, false
		}
		return tech(root, objectId, dataset)
	}
}
