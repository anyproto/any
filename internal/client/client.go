package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/anyproto/any/internal/api"
)

const DefaultAddr = "127.0.0.1:7001"

type Client struct {
	base string
	http *http.Client
	// controlToken, when set, rides every request as the managed-mode
	// control header. A standalone server ignores it.
	controlToken string
}

func New(addr string, timeout time.Duration) *Client {
	if addr == "" {
		addr = DefaultAddr
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		base: "http://" + addr,
		http: &http.Client{Timeout: timeout},
	}
}

// WithControlToken attaches the managed-mode control token to every
// request the client makes. Returns the client for chaining.
func (c *Client) WithControlToken(token string) *Client {
	c.controlToken = token
	return c
}

// newRequest builds a request against the server, stamping the
// control token when one is configured. Every request path goes
// through here so the header can never be forgotten on a new one.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if c.controlToken != "" {
		req.Header.Set(api.ControlTokenHeader, c.controlToken)
	}
	return req, nil
}

func (c *Client) Health(ctx context.Context) (*api.HealthResponse, error) {
	var out api.HealthResponse
	if err := c.do(ctx, http.MethodGet, "/v1/health", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/shutdown", nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}

	req, err := c.newRequest(ctx, method, path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return &TransportError{Addr: c.base, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return parseServerError(resp)
	}

	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}
