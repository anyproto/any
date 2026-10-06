package cli

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/anyproto/any/internal/client"
)

func TestExitCode(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"user", errors.New("bad flag"), 1},
		{"4xx", &client.ServerError{Status: 404}, 1},
		{"5xx", &client.ServerError{Status: 500}, 2},
		{"cut stream", fmt.Errorf("export: %w", &client.StreamError{Err: io.ErrUnexpectedEOF}), 2},
		{"unreachable", &client.TransportError{Err: errors.New("refused")}, 3},
	} {
		if got := exitCode(tc.err); got != tc.want {
			t.Errorf("%s: exitCode = %d, want %d", tc.name, got, tc.want)
		}
	}
}
