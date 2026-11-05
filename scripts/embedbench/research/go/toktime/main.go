//go:build ignore

// toktime: llama.cpp tokenizer cost from Go (yzma), vocab-only load:
// load time and RSS, tokens/s per corpus group, and the cost of
// TokenToPiece offsets for a tokenize-once splitter.
//
//	go run scripts/embedbench/research/go/toktime/main.go <libdir> <model.gguf> <texts.json>
//
// texts.json: [[group, text], ...] (lenexp_prep.py --texts writes one).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

func maxRSSMB() float64 {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return float64(ru.Maxrss) / 1048576 // bytes on darwin
}

func main() {
	lib, gguf, texts := os.Args[1], os.Args[2], os.Args[3]
	var in [][2]string
	b, _ := os.ReadFile(texts)
	if err := json.Unmarshal(b, &in); err != nil {
		panic(err)
	}
	if err := llama.Load(lib); err != nil {
		panic(err)
	}
	llama.Init()
	before := maxRSSMB()
	t0 := time.Now()
	mp := llama.ModelDefaultParams()
	mp.VocabOnly = 1
	m, err := llama.ModelLoadFromFile(gguf, mp)
	if err != nil {
		panic(err)
	}
	vocab := llama.ModelGetVocab(m)
	fmt.Printf("vocab-only load %.0f ms, max RSS %.0f -> %.0f MB, n_vocab %d\n",
		float64(time.Since(t0).Microseconds())/1000, before, maxRSSMB(), llama.VocabNTokens(vocab))

	per := map[string][3]float64{} // tokens, bytes, ns
	var lat []float64
	var totTok, totBytes int
	start := time.Now()
	for _, x := range in {
		t1 := time.Now()
		toks := llama.Tokenize(vocab, x[1], true, true)
		ns := float64(time.Since(t1).Nanoseconds())
		p := per[x[0]]
		p[0] += float64(len(toks))
		p[1] += float64(len(x[1]))
		p[2] += ns
		per[x[0]] = p
		lat = append(lat, ns/1e6)
		totTok += len(toks)
		totBytes += len(x[1])
	}
	el := time.Since(start).Seconds()
	fmt.Printf("tokenize: %d tokens, %.2f MB in %.0f ms = %.2f M tokens/s, %.0f ms/MB\n",
		totTok, float64(totBytes)/1e6, el*1000, float64(totTok)/el/1e6, el*1000/(float64(totBytes)/1e6))
	keys := []string{}
	for k := range per {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		p := per[k]
		fmt.Printf("  %-8s %8.0f tokens %6.2f MB  %6.0f ms/MB  %.3f tokens/byte\n", k, p[0], p[1]/1e6, p[2]/1e6/(p[1]/1e6), p[0]/p[1])
	}
	sort.Float64s(lat)
	fmt.Printf("per-record ms: p50 %.3f p99 %.3f max %.1f\n", lat[len(lat)/2], lat[len(lat)*99/100], lat[len(lat)-1])

	// offsets for a tokenize-once splitter: one TokenToPiece per token
	buf := make([]byte, 64)
	t2 := time.Now()
	pieces := 0
	for _, x := range in {
		for _, t := range llama.Tokenize(vocab, x[1], false, false) {
			n := llama.TokenToPiece(vocab, t, buf, 0, false)
			pieces += int(n)
		}
	}
	el2 := time.Since(t2).Seconds()
	fmt.Printf("tokenize + TokenToPiece per token: %.0f ms total (%.0f ms/MB), piece bytes %d of %d\n",
		el2*1000, el2*1000/(float64(totBytes)/1e6), pieces, totBytes)
	fmt.Printf("max RSS at end %.0f MB\n", maxRSSMB())
}
