package client

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/anyproto/any/internal/api"
)

// SSEFrame is one parsed Server-Sent Events frame. Comment frames
// (lines starting with `:`) are skipped by StreamSSE; only data
// frames surface here. Event is the value of the `event:` line ("" if
// the server omitted it). Id is the value of the `id:` line. Data is
// the joined `data:` lines, newline-separated when there were several.
type SSEFrame struct {
	Event string
	Id    string
	Data  []byte
}

// StreamObjectsQuerySubscribe opens
// POST /v1/spaces/:spaceId/objects/query/subscribe with body as the
// query (filter / sort / limit / offset / includeTotal /
// mailboxCapacity / driftBudgetPercent) and dispatches every SSE
// frame to fn. Blocks until the server sends a `closed` frame, the
// body returns EOF, or ctx is canceled.
func (c *Client) StreamObjectsQuerySubscribe(ctx context.Context, spaceId string, body []byte, fn func(SSEFrame) error) error {
	path := fmt.Sprintf("/v1/spaces/%s/objects/query/subscribe", url.PathEscape(spaceId))
	return c.streamSSE(ctx, http.MethodPost, path, body, fn)
}

// StreamQuerySubscribe opens POST /v1/spaces/:spaceId/query/subscribe
// with body containing objectId + dataset + standard query fields.
func (c *Client) StreamQuerySubscribe(ctx context.Context, spaceId string, body []byte, fn func(SSEFrame) error) error {
	path := fmt.Sprintf("/v1/spaces/%s/query/subscribe", url.PathEscape(spaceId))
	return c.streamSSE(ctx, http.MethodPost, path, body, fn)
}

// streamSSE issues an HTTP request to path with the given method and
// optional body, and parses the SSE response into frames. The shared
// *http.Client carries a request timeout that's useless for long-
// lived streams, so we use a fresh client without one — the caller's
// ctx is the only deadline.
func (c *Client) streamSSE(ctx context.Context, method, path string, body []byte, fn func(SSEFrame) error) error {
	var reqBody io.Reader
	if len(body) > 0 {
		reqBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := streamHTTP.Do(req)
	if err != nil {
		return &TransportError{Addr: c.base, Err: err}
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return parseServerError(resp)
	}
	defer resp.Body.Close()

	return parseSSEStream(resp.Body, fn)
}

// streamHTTP is the long-lived client used for SSE. Distinct from
// Client.http because that one carries a per-request timeout.
var streamHTTP = &http.Client{}

// parseSSEStream reads the SSE wire format off r, accumulating fields
// across lines and dispatching one frame per blank-line terminator.
// Comment lines (starting with `:`) are skipped per the spec.
func parseSSEStream(r io.Reader, fn func(SSEFrame) error) error {
	br := bufio.NewReader(r)
	var (
		event string
		id    string
		data  bytes.Buffer
	)
	flush := func() error {
		if data.Len() == 0 && event == "" && id == "" {
			return nil
		}
		// Trim the trailing newline left by data.WriteByte('\n')
		// joining when only one data line was present.
		raw := data.Bytes()
		if len(raw) > 0 && raw[len(raw)-1] == '\n' {
			raw = raw[:len(raw)-1]
		}
		frame := SSEFrame{Event: event, Id: id, Data: append([]byte(nil), raw...)}
		event, id = "", ""
		data.Reset()
		return fn(frame)
	}
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			// strip trailing CR / LF
			if line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			if len(line) == 0 {
				if err := flush(); err != nil {
					return err
				}
			} else if line[0] == ':' {
				// comment frame — heartbeat etc.
			} else {
				field, value := splitSSEField(line)
				switch field {
				case "event":
					event = string(value)
				case "id":
					id = string(value)
				case "data":
					data.Write(value)
					data.WriteByte('\n')
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				_ = flush()
				return nil
			}
			return err
		}
	}
}

// splitSSEField parses one SSE line into (field, value). The spec
// trims a single leading space from value when present.
func splitSSEField(line []byte) (string, []byte) {
	idx := bytes.IndexByte(line, ':')
	if idx < 0 {
		return string(line), nil
	}
	value := line[idx+1:]
	if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}
	return string(line[:idx]), value
}

// QuerySubscribeFrame is a typed view over an SSEFrame from a
// query/subscribe stream. Exactly one of Ready/Snapshot/Changes/Closed
// is non-nil. Unknown event types pass through with Type set to the
// raw event name and Data unset — additive future events stay
// consumable without a client update.
type QuerySubscribeFrame struct {
	Type     string
	Ready    *api.SubscribeReady
	Snapshot *api.QuerySubscribeSnapshot
	Changes  []api.QuerySubscribeEvent
	Closed   *api.SubscribeClosed
}
