package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"

	"github.com/anyproto/any/internal/api"
)

// TransportError wraps any failure to reach the server (connection refused,
// DNS, timeout). Maps to CLI exit code 3.
type TransportError struct {
	Addr string
	Err  error
}

func (e *TransportError) Error() string {
	addr := strings.TrimPrefix(e.Addr, "http://")
	if e.IsConnectionRefused() {
		return fmt.Sprintf("cannot reach server at %s\n  start it with `any run` in another terminal", addr)
	}
	return fmt.Sprintf("cannot reach server at %s: %v", addr, e.Err)
}

func (e *TransportError) Unwrap() error { return e.Err }

// IsConnectionRefused reports whether the transport error is "server not
// running" as opposed to a mid-flight network failure. The CLI prints a
// specific hint in that case.
func (e *TransportError) IsConnectionRefused() bool {
	return errors.Is(e.Err, syscall.ECONNREFUSED)
}

// ServerError is a 4xx/5xx response parsed out of the canonical envelope.
// Maps to CLI exit code 1 (4xx) or 2 (5xx).
type ServerError struct {
	Status int
	API    api.APIError
}

func (e *ServerError) Error() string {
	if e.API.Message != "" {
		return fmt.Sprintf("%s: %s (%s)", http.StatusText(e.Status), e.API.Message, e.API.Code)
	}
	return fmt.Sprintf("%s (%d)", http.StatusText(e.Status), e.Status)
}

func (e *ServerError) IsClient() bool { return e.Status >= 400 && e.Status < 500 }
func (e *ServerError) IsServer() bool { return e.Status >= 500 }

func parseServerError(resp *http.Response) error {
	raw, _ := io.ReadAll(resp.Body)
	out := &ServerError{Status: resp.StatusCode}
	if len(raw) > 0 {
		var env api.ErrorEnvelope
		if err := json.Unmarshal(raw, &env); err == nil {
			out.API = env.Error
		}
	}
	if out.API.Code == "" {
		out.API.Code = "unknown"
		out.API.Message = http.StatusText(resp.StatusCode)
	}
	return out
}
