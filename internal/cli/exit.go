package cli

import (
	"errors"

	"github.com/anyproto/any/internal/client"
)

// exitCode maps an error to the v1 exit-code scheme:
//
//	0 — success (never reached here)
//	1 — user / 4xx
//	2 — server / 5xx
//	3 — transport (can't reach server)
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var transport *client.TransportError
	if errors.As(err, &transport) {
		return 3
	}
	var server *client.ServerError
	if errors.As(err, &server) {
		if server.IsServer() {
			return 2
		}
		return 1
	}
	return 1
}
