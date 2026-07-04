package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestControlServerContract drives the real startControlServer handlers over
// HTTP. It doesn't need a running `any` server: base points at an unreachable
// address, so bootstrapSystemFiles fails fast and we assert on the dispatch +
// error-shaping contract (method rejection, JSON reply shape) rather than a
// successful sync. Verifies the SIGHUP/SIGUSR1 → HTTP replacement wire surface.
func TestControlServerContract(t *testing.T) {
	// Unreachable API so bootstrapSystemFiles returns an error quickly.
	base = "http://127.0.0.1:1"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	go startControlServer(addr, "space-x", "prog-type", "skill-type")

	// Wait for the listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c, err := net.Dial("tcp", addr)
		if err == nil {
			c.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control server did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, ep := range []string{"/bootstrap", "/bootstrap-clean"} {
		// GET is rejected before any bootstrap work — 405.
		resp, err := http.Get("http://" + addr + ep)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: got %d, want 405", ep, resp.StatusCode)
		}

		// POST dispatches to the handler; the bootstrap fails against the
		// unreachable API, so we expect a non-2xx JSON {error:...} reply.
		resp, err = http.Post("http://"+addr+ep, "application/json", nil)
		if err != nil {
			t.Fatalf("POST %s: %v", ep, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("POST %s: got %d, want >=400 (unreachable API)", ep, resp.StatusCode)
		}
		var out controlResponse
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("POST %s: reply not JSON controlResponse: %v (%s)", ep, err, body)
		}
		if out.Error == "" {
			t.Errorf("POST %s: expected error field set, got %s", ep, body)
		}
	}
}
