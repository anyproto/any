//go:build ignore

// lenexp: does a 512-token cap change EG2 vs Qwen3? Same chunks and
// queries, each model with its production prompts, pooling and tokenizer
// (internal/indexer/embed_local.go settings, Metal, one doc per decode).
// Strategies per chunk: full (<=2048 tokens), trunc512, mean512
// (<=512-token segments at sentence/line ends, token-weighted mean),
// mean512eq (equal-size segments), each also with query-side centering
// ("+c": q' = norm(q - 0.5*mu), mu = mean of that strategy's doc vectors).
// Writes <lenexp>.<kind>.ranks.json for lenan.py / lenan2.py.
//
//	go run scripts/embedbench/research/go/lenexp/main.go <libdir> <model.gguf> <eg2|qwen3> <lenexp.json>
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hybridgroup/yzma/pkg/llama"
)

type chunk struct {
	Lang, Text string
	EG2Tokens  int `json:"eg2Tokens"`
}
type query struct {
	Lang, Kind, Text string
	Target           int
}

var (
	vocab  llama.Vocab
	lctx   llama.Context
	nEmbd  int32
	eos    llama.Token
	docPfx string
	qryPfx string
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

func embed(toks []llama.Token) []float64 {
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
	raw, err := llama.GetEmbeddingsSeq(lctx, 0, nEmbd)
	if err != nil {
		panic(err)
	}
	v := make([]float64, len(raw))
	for i, x := range raw {
		v[i] = float64(x)
	}
	return norm(v)
}

func norm(v []float64) []float64 {
	s := 0.0
	for _, x := range v {
		s += x * x
	}
	s = math.Sqrt(s)
	for i := range v {
		v[i] /= s
	}
	return v
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

var unitRe = regexp.MustCompile(`(?s).*?(?:[.!?。！？]+\s*|\n+|$)`)

// segments cuts text into pieces whose doc-prompted token count stays <= cap,
// at sentence or line ends; a unit over the cap alone is cut by tokens later.
func segments(text string, cap int) []string {
	var out []string
	cur := ""
	for _, u := range unitRe.FindAllString(text, -1) {
		if u == "" {
			continue
		}
		if cur != "" && len(tokenize(docPfx+cur+u)) > cap {
			out = append(out, strings.TrimSpace(cur))
			cur = ""
		}
		cur += u
	}
	if strings.TrimSpace(cur) != "" {
		out = append(out, strings.TrimSpace(cur))
	}
	return out
}

func main() {
	lib, gguf, kind, in := os.Args[1], os.Args[2], os.Args[3], os.Args[4]
	var data struct {
		Chunks  []chunk
		Queries []query
	}
	b, _ := os.ReadFile(in)
	if err := json.Unmarshal(b, &data); err != nil {
		panic(err)
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
	if kind == "eg2" {
		cp.PoolingType = llama.PoolingTypeMean
		docPfx, qryPfx = "title: none | text: ", "task: search result | query: "
	} else {
		cp.PoolingType = llama.PoolingTypeUnspecified
		qryPfx = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:"
	}
	lctx, err = llama.InitFromModel(model, cp)
	if err != nil {
		panic(err)
	}
	vocab = llama.ModelGetVocab(model)
	nEmbd = llama.ModelNEmbdOut(model)
	eos = llama.VocabEOS(vocab)

	t0 := time.Now()
	const capTok = 512
	n := len(data.Chunks)
	full, tr, mean, meanEq := make([][]float64, n), make([][]float64, n), make([][]float64, n), make([][]float64, n)
	var segLens, greedyLens []int
	over, ntoks := 0, make([]int, n)
	for i, c := range data.Chunks {
		toks := tokenize(docPfx + c.Text)
		ntoks[i] = len(toks)
		full[i] = embed(trunc(toks, 2048))
		if len(toks) <= capTok {
			tr[i], mean[i], meanEq[i] = full[i], full[i], full[i]
			continue
		}
		over++
		tr[i] = embed(trunc(toks, capTok))
		acc := make([]float64, len(full[i]))
		for _, s := range segments(c.Text, capTok) {
			st := trunc(tokenize(docPfx+s), capTok)
			v := embed(st)
			for k := range acc {
				acc[k] += float64(len(st)) * v[k]
			}
		}
		mean[i] = norm(acc)
		k := (len(toks) + capTok - 1) / capTok
		target := (len(toks)+k-1)/k + 24
		if target > capTok {
			target = capTok
		}
		acc2 := make([]float64, len(full[i]))
		for _, s := range segments(c.Text, target) {
			st := trunc(tokenize(docPfx+s), capTok)
			v := embed(st)
			segLens = append(segLens, len(st))
			for x := range acc2 {
				acc2[x] += float64(len(st)) * v[x]
			}
		}
		meanEq[i] = norm(acc2)
		for _, s := range segments(c.Text, capTok) {
			greedyLens = append(greedyLens, len(trunc(tokenize(docPfx+s), capTok)))
		}
	}
	qv := make([][]float64, len(data.Queries))
	for i, q := range data.Queries {
		qv[i] = embed(trunc(tokenize(qryPfx+q.Text), capTok))
	}
	sort.Ints(append([]int{}, ntoks...))
	fmt.Printf("%s: %d chunks (%d over %d %s tokens), %d queries, %.0f s\n", kind, n, over, capTok, kind, len(data.Queries), time.Since(t0).Seconds())

	var cT, cM []float64
	for i := range full {
		if ntoks[i] > capTok {
			cT = append(cT, dot(tr[i], full[i]))
			cM = append(cM, dot(mean[i], full[i]))
		}
	}
	fmt.Printf("  cos to full, over-cap chunks: trunc %.4f, mean %.4f\n", avg(cT), avg(cM))

	short := func(l []int) string {
		sort.Ints(l)
		lt := 0
		for _, x := range l {
			if x < 80 {
				lt++
			}
		}
		return fmt.Sprintf("n=%d min=%d p10=%d <80tok=%d", len(l), l[0], l[len(l)/10], lt)
	}
	fmt.Printf("  segments greedy: %s\n  segments equal:  %s\n", short(greedyLens), short(segLens))
	base := map[string][][]float64{"full": full, "trunc512": tr, "mean512": mean, "mean512eq": meanEq}
	sets := map[string][][]float64{}
	centered := map[string][][]float64{}
	for name, V := range base {
		sets[name], sets[name+"+c"] = V, V
		mu := make([]float64, len(V[0]))
		for _, v := range V {
			for k := range mu {
				mu[k] += v[k] / float64(len(V))
			}
		}
		cq := make([][]float64, len(qv))
		for qi := range qv {
			x := make([]float64, len(mu))
			for k := range x {
				x[k] = qv[qi][k] - 0.5*mu[k]
			}
			cq[qi] = norm(x)
		}
		centered[name] = cq
	}
	// ranks: dump for paired analysis
	type row struct {
		Kind, Lang string
		Rank       map[string]int
	}
	var rows []row
	for qi, q := range data.Queries {
		r := row{q.Kind, q.Lang, map[string]int{}}
		for name, V := range sets {
			qq := qv[qi]
			if strings.HasSuffix(name, "+c") {
				qq = centered[strings.TrimSuffix(name, "+c")][qi]
			}
			t := dot(V[q.Target], qq)
			rank := 1
			for j := range V {
				if j != q.Target && dot(V[j], qq) > t {
					rank++
				}
			}
			r.Rank[name] = rank
		}
		rows = append(rows, r)
	}
	out, _ := json.Marshal(map[string]any{"kind": kind, "tokens": ntoks, "rows": rows})
	_ = os.WriteFile(strings.TrimSuffix(in, ".json")+"."+kind+".ranks.json", out, 0o644)
}

func avg(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}
