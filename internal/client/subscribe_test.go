package client

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// A frame the stream ends inside is discarded and the stream reports
// the cut; only blank-line terminated frames reach the callback.
func TestParseSSEStream_UnterminatedFrameIsACut(t *testing.T) {
	stream := ": keepalive\n\nevent: ready\ndata: {}\n\nevent: changes\ndata: [1,"
	var got []SSEFrame
	err := parseSSEStream(strings.NewReader(stream), func(f SSEFrame) error {
		got = append(got, f)
		return nil
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if len(got) != 1 || got[0].Event != "ready" || string(got[0].Data) != "{}" {
		t.Fatalf("frames = %+v, want only the terminated ready frame", got)
	}
}

// A last line without its newline is a cut even when it starts a frame.
func TestParseSSEStream_UnterminatedLineIsACut(t *testing.T) {
	err := parseSSEStream(strings.NewReader("event: a\ndata: 1\n\neve"), func(SSEFrame) error { return nil })
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestParseSSEStream_CleanEnd(t *testing.T) {
	var got int
	err := parseSSEStream(strings.NewReader("event: a\ndata: 1\n\n: keepalive\n"), func(SSEFrame) error {
		got++
		return nil
	})
	if err != nil || got != 1 {
		t.Fatalf("frames = %d, err = %v; want 1, nil", got, err)
	}
}

func TestParseSSEStream_ReturnsCallbackError(t *testing.T) {
	stop := errors.New("stop")
	err := parseSSEStream(strings.NewReader("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"), func(SSEFrame) error {
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want the callback's error", err)
	}
}
