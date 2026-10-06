package client

import (
	"errors"
	"strings"
	"testing"
)

// A frame the stream ends inside is discarded: only blank-line
// terminated frames reach the callback.
func TestParseSSEStream_DropsUnterminatedFrame(t *testing.T) {
	stream := ": keepalive\n\nevent: ready\ndata: {}\n\nevent: changes\ndata: [1,"
	var got []SSEFrame
	err := parseSSEStream(strings.NewReader(stream), func(f SSEFrame) error {
		got = append(got, f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event != "ready" || string(got[0].Data) != "{}" {
		t.Fatalf("frames = %+v, want only the terminated ready frame", got)
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
