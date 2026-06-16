// Command libany is throwaway measurement scaffolding for IOS-6169
// (SPIKE 0.2 / 0.3). It links the full embedded-server dependency graph
// into a c-archive so the linked Mach-O can be measured for forbidden
// symbols and __TEXT size. The polished C surface is a later phase; this
// is deliberately minimal — one //export that references the real boot
// path so the linker pulls in any-sync + libp2p + QUIC + the modernc
// SQLite shim, i.e. a realistic link, not a hello-world.
package main

import "C"

import (
	"context"

	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/server"
)

//export LibanyRun
func LibanyRun(dataDir *C.char, addr *C.char) C.int {
	cfg := config.Defaults()
	cfg.DataDir = C.GoString(dataDir)
	cfg.Listen.Addr = C.GoString(addr)
	cfg.Index.Embedder = "none"

	if err := server.RunWithListener(context.Background(), cfg, func(string) {}); err != nil {
		return 1
	}
	return 0
}

func main() {}
