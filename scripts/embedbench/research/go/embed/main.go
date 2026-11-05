//go:build ignore

// embed: embeds JSONL texts with EG2 or Qwen3 through llama.cpp (Metal),
// production tokenization (addSpecial, truncate with EOS last).
//
//	go run scripts/embedbench/research/go/embed/main.go <libdir> <model.gguf> <eg2|qwen3> <in.jsonl> <out.f32> [flags]
//	  -prefix S      prompt prefix (default: the model's production doc prompt)
//	  -query         use the model's production query prefix instead
//	  -field F       JSON field to embed (default text)
//	  -title         EG2: build 'title: <title|none> | text: ' from the line's title
//	  -pool mean|last|tokens   tokens = per-token states pooled four ways:
//	                 out.f32 (mean of all), out.noprompt.f32 (mean without the
//	                 prefix tokens, bos included), out.nospecial.f32 (mean
//	                 without bos/eos), out.lasttok.f32 (last token)
//	  -maxlen N      token cap (default 2048)
//	  -limit N       first N rows only
//
// Output: float32 rows [n, dim], L2-normalized; out.ntok int32 token counts.
package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

var (
	vocab llama.Vocab
	lctx  llama.Context
	nEmbd int32
	eos   llama.Token
)

func tokenize(s string) []llama.Token {
	t := llama.Tokenize(vocab, s, true, true)
	if len(t) == 0 {
		t = []llama.Token{eos}
	}
	return t
}

func trunc(t []llama.Token, n int) []llama.Token {
	if len(t) <= n {
		return t
	}
	out := append([]llama.Token{}, t[:n-1]...)
	return append(out, eos)
}

func norm(v []float32) []float32 {
	s := 0.0
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	s = math.Sqrt(s)
	if s == 0 {
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / s)
	}
	return v
}

func decode(toks []llama.Token) {
	b := llama.BatchInit(int32(len(toks)), 0, 1)
	defer llama.BatchFree(b)
	for i, t := range toks {
		b.Add(t, llama.Pos(i), []llama.SeqId{0}, true)
	}
	if mem, err := llama.GetMemory(lctx); err == nil {
		_ = llama.MemoryClear(mem, true)
	}
	if _, err := llama.Decode(lctx, b); err != nil {
		panic(err)
	}
}

func main() {
	fs := flag.NewFlagSet("embed", flag.ExitOnError)
	prefix := fs.String("prefix", "\x00", "")
	query := fs.Bool("query", false, "")
	field := fs.String("field", "text", "")
	title := fs.Bool("title", false, "")
	pool := fs.String("pool", "", "")
	maxlen := fs.Int("maxlen", 2048, "")
	limit := fs.Int("limit", 0, "")
	lib, gguf, kind, in, out := os.Args[1], os.Args[2], os.Args[3], os.Args[4], os.Args[5]
	_ = fs.Parse(os.Args[6:])

	docPfx, qryPfx := "", ""
	defPool := "last"
	if kind == "eg2" {
		docPfx, qryPfx = "title: none | text: ", "task: search result | query: "
		defPool = "mean"
	} else {
		qryPfx = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:"
	}
	if *pool == "" {
		*pool = defPool
	}
	pfx := docPfx
	if *query {
		pfx = qryPfx
	}
	if *prefix != "\x00" {
		pfx = *prefix
	}

	if err := llama.Load(lib); err != nil {
		panic(err)
	}
	llama.Init()
	mp := llama.ModelDefaultParams()
	mp.NGpuLayers = 99
	model, err := llama.ModelLoadFromFile(gguf, mp)
	if err != nil {
		panic(err)
	}
	cp := llama.ContextDefaultParams()
	cp.NCtx, cp.NBatch, cp.NUbatch, cp.NSeqMax = 2048, 2048, 2048, 1
	cp.Embeddings = 1
	cp.OpOffload = 1
	switch *pool {
	case "mean":
		cp.PoolingType = llama.PoolingTypeMean
	case "last":
		cp.PoolingType = llama.PoolingTypeLast
	case "tokens":
		cp.PoolingType = llama.PoolingTypeNone
	default:
		panic("pool")
	}
	lctx, err = llama.InitFromModel(model, cp)
	if err != nil {
		panic(err)
	}
	vocab = llama.ModelGetVocab(model)
	nEmbd = llama.ModelNEmbdOut(model)
	eos = llama.VocabEOS(vocab)

	f, err := os.Open(in)
	if err != nil {
		panic(err)
	}
	type row struct {
		Title string `json:"title"`
		Text  string `json:"text"`
		Extra map[string]any
	}
	var rows []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			panic(err)
		}
		rows = append(rows, m)
		if *limit > 0 && len(rows) >= *limit {
			break
		}
	}
	of, _ := os.Create(out)
	ow := bufio.NewWriterSize(of, 1<<20)
	nf, _ := os.Create(out + ".ntok")
	nw := bufio.NewWriter(nf)
	var vw [3]*bufio.Writer
	if *pool == "tokens" {
		for i, suf := range []string{".noprompt.f32", ".nospecial.f32", ".lasttok.f32"} {
			vf, _ := os.Create(out + suf)
			vw[i] = bufio.NewWriterSize(vf, 1<<20)
			defer func(w *bufio.Writer, f *os.File) { w.Flush(); f.Close() }(vw[i], vf)
		}
	}
	t0 := time.Now()
	for i, r := range rows {
		p := pfx
		if *title && !*query {
			t, _ := r["title"].(string)
			if t == "" {
				t = "none"
			}
			p = "title: " + t + " | text: "
		}
		text, _ := r[*field].(string)
		toks := trunc(tokenize(p+text), *maxlen)
		decode(toks)
		_ = binary.Write(nw, binary.LittleEndian, int32(len(toks)))
		if *pool == "tokens" {
			// prefix tokens: tokenize(prefix) = bos + prefix + eos; skip = all but eos
			skip := len(tokenize(p)) - 1
			if skip >= len(toks)-1 {
				skip = 0
			}
			acc := make([]float32, nEmbd)
			accNP := make([]float32, nEmbd)
			accNS := make([]float32, nEmbd)
			var last []float32
			for j := range toks {
				v, err := llama.GetEmbeddingsIth(lctx, int32(j), nEmbd)
				if err != nil {
					panic(err)
				}
				for k := range acc {
					acc[k] += v[k]
					if j >= skip {
						accNP[k] += v[k]
					}
					if j > 0 && j < len(toks)-1 {
						accNS[k] += v[k]
					}
				}
				last = append([]float32{}, v...)
			}
			_ = binary.Write(ow, binary.LittleEndian, norm(acc))
			_ = binary.Write(vw[0], binary.LittleEndian, norm(accNP))
			_ = binary.Write(vw[1], binary.LittleEndian, norm(accNS))
			_ = binary.Write(vw[2], binary.LittleEndian, norm(last))
		} else {
			v, err := llama.GetEmbeddingsSeq(lctx, 0, nEmbd)
			if err != nil {
				panic(err)
			}
			_ = binary.Write(ow, binary.LittleEndian, norm(append([]float32{}, v...)))
		}
		if i%1000 == 0 && i > 0 {
			fmt.Fprintf(os.Stderr, "%d/%d %.0fs\n", i, len(rows), time.Since(t0).Seconds())
		}
	}
	ow.Flush()
	of.Close()
	nw.Flush()
	nf.Close()
	fmt.Fprintf(os.Stderr, "%s: %d rows, dim %d, %.0fs\n", out, len(rows), nEmbd, time.Since(t0).Seconds())
}
