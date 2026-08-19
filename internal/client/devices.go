package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// DevicesList fetches the account's device registry — one row per
// device (peer) plus the per-app active winners resolved by the
// canonical election rule. GET /v1/devices.
func (c *Client) DevicesList(ctx context.Context) (*api.DevicesListResponse, error) {
	var out api.DevicesListResponse
	if err := c.do(ctx, http.MethodGet, "/v1/devices", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DevicesQuery runs a windowed snapshot over the raw `devices` rows
// (POST /v1/devices/query). body carries the standard query fields
// (filter / sort / limit / offset / includeTotal).
func (c *Client) DevicesQuery(ctx context.Context, body []byte) (*api.QueryResponse, error) {
	var out api.QueryResponse
	var reqBody any
	if len(body) > 0 {
		reqBody = json.RawMessage(body)
	}
	if err := c.do(ctx, http.MethodPost, "/v1/devices/query", reqBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// StreamDevicesQuerySubscribe opens POST /v1/devices/query/subscribe —
// the windowed live view over the device registry. An empty body is a
// full snapshot + live stream.
func (c *Client) StreamDevicesQuerySubscribe(ctx context.Context, body []byte, fn func(SSEFrame) error) error {
	return c.streamSSE(ctx, http.MethodPost, "/v1/devices/query/subscribe", body, fn)
}

// DeviceUpdateMe updates THIS device's registry row (name, installed
// apps). PUT /v1/devices/me.
func (c *Client) DeviceUpdateMe(ctx context.Context, req api.DeviceUpdateRequest) error {
	return c.do(ctx, http.MethodPut, "/v1/devices/me", req, nil)
}

// DeviceActivate claims the active role for one app slug on THIS
// device. POST /v1/devices/activate.
func (c *Client) DeviceActivate(ctx context.Context, app string) error {
	return c.do(ctx, http.MethodPost, "/v1/devices/activate", api.DeviceActivateRequest{App: app}, nil)
}

// DeviceDelete prunes a device's registry row. Permanent for that
// peerId — record tombstones are sticky, so a deleted device can never
// re-register without re-deriving peer keys. DELETE /v1/devices/:peerId.
func (c *Client) DeviceDelete(ctx context.Context, peerId string) error {
	return c.do(ctx, http.MethodDelete, "/v1/devices/"+url.PathEscape(peerId), nil, nil)
}
