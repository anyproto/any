package indexer

import (
	"encoding/json"
	"os"
	"sync"
)

// experiment only: ANY_EXP_DUMP_LEGS=<file> appends each search's title
// pass, per-leg chunk rankings and final page as one JSON line, so
// fusion can be replayed offline. ANY_EXP_DUMP_DATA=1 adds chunk text.
var expDump struct {
	once sync.Once
	mu   sync.Mutex
	f    *os.File
	data bool
}

type expHit struct {
	O string  `json:"o"`
	D string  `json:"d"`
	R string  `json:"r"`
	C int     `json:"c"`
	S float64 `json:"s"`
	M string  `json:"m,omitempty"`
	T string  `json:"t,omitempty"`
}

func expDumpLegs(spaceId, query, mode string, limit int, titles, fts, vec []Hit, groups []Group) {
	path := os.Getenv("ANY_EXP_DUMP_LEGS")
	if path == "" {
		return
	}
	expDump.once.Do(func() {
		expDump.f, _ = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		expDump.data = os.Getenv("ANY_EXP_DUMP_DATA") == "1"
	})
	if expDump.f == nil {
		return
	}
	conv := func(hs []Hit, data bool) []expHit {
		out := make([]expHit, 0, len(hs))
		for _, h := range hs {
			e := expHit{O: h.ObjectId, D: h.Dataset, R: h.RecordId, C: h.Chunk, S: h.Score, M: h.Match}
			if data {
				e.T = h.Data
			}
			out = append(out, e)
		}
		return out
	}
	page := make([]Hit, 0, len(groups))
	for _, g := range groups {
		page = append(page, g.Hit)
	}
	line, err := json.Marshal(map[string]any{
		"space": spaceId, "query": query, "mode": mode, "limit": limit,
		"titles": conv(titles, false), "fts": conv(fts, expDump.data), "vec": conv(vec, expDump.data),
		"page": conv(page, false),
	})
	if err != nil {
		return
	}
	expDump.mu.Lock()
	expDump.f.Write(append(line, '\n'))
	expDump.mu.Unlock()
}
