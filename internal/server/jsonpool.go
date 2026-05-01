package server

import (
	"sync"

	"github.com/anyproto/any-store/anyenc"
	"github.com/valyala/fastjson"
)

// fastjsonParserPool reuses *fastjson.Parser across requests. Each parser
// retains scratch buffers between Parse calls; ensure callers do not
// outlive the scope they returned the parser in. The handlers fully
// finish reading the parsed value before Put-ing the parser back.
var fastjsonParserPool = sync.Pool{
	New: func() any { return &fastjson.Parser{} },
}

// fastjsonArenaPool reuses *fastjson.Arena for marshalling anyenc values
// to JSON bytes (record renders, error payloads).
var fastjsonArenaPool = sync.Pool{
	New: func() any { return &fastjson.Arena{} },
}

// anyencArenaPool is reserved for handlers that need to construct
// anyenc.Value trees outside of an SDK call. The Arena is reset before
// reuse so the per-arena allocator stays bounded.
var anyencArenaPool = sync.Pool{
	New: func() any { return &anyenc.Arena{} },
}

func getFastjsonParser() *fastjson.Parser { return fastjsonParserPool.Get().(*fastjson.Parser) }
func putFastjsonParser(p *fastjson.Parser) { fastjsonParserPool.Put(p) }

func getFastjsonArena() *fastjson.Arena {
	a := fastjsonArenaPool.Get().(*fastjson.Arena)
	a.Reset()
	return a
}
func putFastjsonArena(a *fastjson.Arena) { fastjsonArenaPool.Put(a) }
