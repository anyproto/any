package client

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
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

// A body the server cuts after a 200 reads as a StreamError; the
// caller's own cancellation does not.
func TestStreamError_CutBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		if r.URL.Query().Get("scope") == "hang" {
			<-r.Context().Done()
			return
		}
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()
	cl := New(strings.TrimPrefix(srv.URL, "http://"), 0)

	body, err := cl.LocalExport(context.Background(), "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(body)
	_ = body.Close()
	var se *StreamError
	if !errors.As(err, &se) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("cut body: err = %v, want a StreamError wrapping io.ErrUnexpectedEOF", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	body, err = cl.LocalExport(ctx, "hang", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(50*time.Millisecond, cancel)
	_, err = io.ReadAll(body)
	_ = body.Close()
	if err == nil || errors.As(err, &se) {
		t.Fatalf("cancelled read: err = %v, want a plain cancellation", err)
	}
}
