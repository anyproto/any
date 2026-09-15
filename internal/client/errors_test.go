package client

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

// A server that is not running reads as a refused connection on every
// platform, so the CLI prints its "start it with `any run`" hint.
func TestTransportError_ConnectionRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = New(addr, 5*time.Second).Health(context.Background())
	var te *TransportError
	if !errors.As(err, &te) {
		t.Fatalf("Health on a closed port: %v, want a TransportError", err)
	}
	if !te.IsConnectionRefused() {
		t.Fatalf("closed port not reported as refused: %v", te.Err)
	}
}
