//go:build vector && !gomobile

package indexer

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/hybridgroup/yzma/pkg/llama"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
)

// Pinned zero-config defaults: `index.embedder: local` alone downloads
// this model into <data-dir>/index/models and embeds with it.
const (
	localModelName   = "Qwen3-Embedding-0.6B-Q8_0.gguf"
	localModelURL    = "https://huggingface.co/Qwen/Qwen3-Embedding-0.6B-GGUF/resolve/main/" + localModelName
	localModelSHA256 = "06507c7b42688469c4e7298b0a1e16deff06caf291cf0a5b278c308249c3e439"
	localDefaultDim  = 1024
	// Truncation bound, not the model maximum (Qwen3 goes to 32k): for
	// embeddings NBatch must cover the whole input, so context size is
	// also the compute/memory bound per text. The bound is per SEQUENCE
	// (maxDocTokens) — batchDocs > 1 divides it. docs/13-index.md
	// § Known limits records the truncation decision.
	localDefaultCtx = 2048
	// Qwen3-Embedding retrieval instruction. Documents embed bare;
	// queries carry the task instruction (skipping it costs a few
	// points of retrieval quality per the model card).
	localQueryPrefix = "Instruct: Given a web search query, retrieve relevant passages that answer the query\nQuery:"
	// Docs packed per llama_decode as parallel sequences. The unified KV
	// cache PARTITIONS the context across NSeqMax, so packing N docs cuts
	// the per-doc token bound to nCtx/N — a wider batch buys throughput
	// only by truncating every text. One doc per decode keeps the whole
	// nCtx available to it, which is what localDefaultCtx documents.
	localDefaultBatchDocs = 1
	// llama.cpp rounds the per-sequence KV allocation up to this many
	// tokens, so a sequence never gets less than one block.
	localSeqTokenBlock = 256
)

// Local embeds in-process through llama.cpp (yzma purego bindings — no
// CGO; the shared libs from `make llamacpp` are dlopened at runtime).
//
// Construction is cheap and never probes: the model download (if
// needed) runs in the background, and lib/model loading happens lazily
// on the first embed call. Until everything is in place EmbedDocs/
// EmbedQuery return ordinary errors, which the embed loop's existing
// outage semantics turn into "vectors stay pending, retry on tick" —
// no special wiring for "model still downloading".
type Local struct {
	modelPath    string
	libDir       string
	nCtx         int
	threads      int // 0 = runtime.NumCPU()-1
	outDim       int // 0 = model dim; >0 = Matryoshka truncate + renormalize
	queryPrefix  string
	gpuLayers    int  // -1 = llama.cpp default (offload all when a GPU is present)
	batchDocs    int  // max docs packed per llama_decode (≥1)
	defaultModel bool // pinned Qwen3 (possibly via mirror URL), not a custom GGUF

	dl *modelDownload // nil when modelPath overridden or file already present

	// One llama context, single-threaded by contract: the mutex
	// serializes lazy init, all decodes, and Close across the per-space
	// embed loops and query-time callers.
	mu     sync.Mutex
	loaded bool
	closed bool
	model  llama.Model
	lctx   llama.Context
	vocab  llama.Vocab
	nEmbd  int32
}

// NewLocal resolves paths and kicks off the background model download
// when the default model isn't on disk yet. It does not touch
// llama.cpp. The model lives in modelsDir (shared across accounts); a
// copy already present in legacyModelsDir (the old per-data-dir
// location) is used as-is so existing downloads aren't repeated.
func NewLocal(cfg config.IndexLocal, modelsDir, legacyModelsDir string, onProcess func(ProcessUpdate)) (*Local, error) {
	l := &Local{
		nCtx:         cfg.ContextSize,
		threads:      cfg.Threads,
		outDim:       cfg.Dim,
		queryPrefix:  cfg.QueryPrefix,
		gpuLayers:    -1,
		batchDocs:    cfg.BatchDocs,
		defaultModel: cfg.ModelPath == "",
	}
	if l.nCtx <= 0 {
		l.nCtx = localDefaultCtx
	}
	if cfg.GpuLayers != nil {
		l.gpuLayers = *cfg.GpuLayers
	}
	if l.batchDocs <= 0 {
		l.batchDocs = localDefaultBatchDocs
	}
	if per := l.maxDocTokens(); per < l.nCtx {
		logger.NewNamed("indexer").Warn("index: local embedder batchDocs truncates texts below contextSize",
			zap.Int("batchDocs", l.batchDocs), zap.Int("contextSize", l.nCtx), zap.Int("maxDocTokens", per))
	}

	l.libDir = cfg.LibDir
	if l.libDir == "" {
		l.libDir = os.Getenv("YZMA_LIB")
	}
	if l.libDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("indexer: local embedder: resolve executable for default libDir: %w", err)
		}
		l.libDir = filepath.Join(filepath.Dir(exe), "llamacpp")
	}

	if cfg.ModelPath != "" {
		// Air-gapped: the file is the user's responsibility, no download.
		l.modelPath = cfg.ModelPath
	} else {
		l.modelPath = filepath.Join(modelsDir, localModelName)
		if legacy := filepath.Join(legacyModelsDir, localModelName); legacyModelsDir != "" {
			if _, err := os.Stat(legacy); err == nil {
				l.modelPath = legacy
			}
		}
		if _, err := os.Stat(l.modelPath); err != nil {
			url, sha := localModelURL, localModelSHA256
			if cfg.ModelUrl != "" {
				url, sha = cfg.ModelUrl, cfg.ModelSha256
				l.defaultModel = false
			}
			l.dl = startModelDownload(url, l.modelPath, sha, nil, onProcess)
		}
	}
	if l.queryPrefix == "" && l.defaultModel {
		l.queryPrefix = localQueryPrefix
	}
	return l, nil
}

// llamaRuntime guards the process-global llama.cpp state (Load + Init
// happen once per process, shared by every Local instance). Failures
// stay retryable: a missing lib dir now may be populated later.
var llamaRuntime struct {
	sync.Mutex
	loaded bool
}

func loadLlamaRuntime(libDir string) error {
	llamaRuntime.Lock()
	defer llamaRuntime.Unlock()
	if llamaRuntime.loaded {
		return nil
	}
	if _, err := os.Stat(libDir); err != nil {
		return fmt.Errorf("indexer: local embedder: llama.cpp libs not found at %s (run 'make llamacpp' or set index.local.libDir): %w", libDir, err)
	}
	if err := llama.Load(libDir); err != nil {
		return fmt.Errorf("indexer: local embedder: load llama.cpp from %s: %w", libDir, err)
	}
	llama.LogSet(llama.LogSilent())
	llama.Init() // loads backend libs (cpu variants / metal) from the same dir
	llamaRuntime.loaded = true
	return nil
}

// ensureLoaded lazily loads libs + model. Caller holds l.mu.
func (l *Local) ensureLoaded() error {
	if l.loaded {
		return nil
	}
	if l.closed {
		return fmt.Errorf("indexer: local embedder: closed")
	}
	if _, err := os.Stat(l.modelPath); err != nil {
		if l.dl != nil {
			if st := l.dl.Status(); st != nil {
				return fmt.Errorf("indexer: local embedder: model not ready: %w", st)
			}
		}
		return fmt.Errorf("indexer: local embedder: model not found at %s", l.modelPath)
	}
	if err := loadLlamaRuntime(l.libDir); err != nil {
		return err
	}

	mp := llama.ModelDefaultParams()
	if l.gpuLayers >= 0 {
		mp.NGpuLayers = int32(l.gpuLayers)
	}
	model, err := llama.ModelLoadFromFile(l.modelPath, mp)
	if err != nil {
		return fmt.Errorf("indexer: local embedder: load model %s: %w", l.modelPath, err)
	}
	if model == 0 {
		return fmt.Errorf("indexer: local embedder: load model %s: not a usable GGUF", l.modelPath)
	}

	threads := int32(l.threadCount())
	cp := llama.ContextDefaultParams()
	cp.NCtx = uint32(l.nCtx)
	// Embeddings need the whole input in one logical/physical batch;
	// a decode packs up to batchDocs sequences into that token budget,
	// each capped at maxDocTokens by tokenize.
	cp.NBatch = uint32(l.nCtx)
	cp.NUbatch = uint32(l.nCtx)
	cp.NSeqMax = uint32(l.batchDocs)
	cp.NThreads = threads
	cp.NThreadsBatch = threads
	cp.PoolingType = llama.PoolingTypeLast // Qwen3-Embedding pools the trailing EOS
	cp.Embeddings = 1
	lctx, err := llama.InitFromModel(model, cp)
	if err != nil {
		_ = llama.ModelFree(model)
		return fmt.Errorf("indexer: local embedder: init context: %w", err)
	}

	l.model, l.lctx = model, lctx
	l.vocab = llama.ModelGetVocab(model)
	l.nEmbd = llama.ModelNEmbd(model)
	l.loaded = true
	return nil
}

// threadCount resolves the llama.cpp compute thread count: the explicit
// config value when set, else runtime.NumCPU()-1 (leave one core free),
// floored at 1.
func (l *Local) threadCount() int {
	if l.threads > 0 {
		return l.threads
	}
	return max(1, runtime.NumCPU()-1)
}

// maxDocTokens is the real per-text bound: the unified KV cache splits
// nCtx across batchDocs sequences, floored to a whole block. Texts are
// truncated to it, so a decode can never be refused for want of a KV
// slot however batchDocs is configured.
func (l *Local) maxDocTokens() int {
	if l.batchDocs <= 1 {
		return l.nCtx // one sequence, the whole context is its slot
	}
	// Floor to a whole block: rounding up would promise more than
	// nCtx/batchDocs when batchDocs does not divide nCtx, which is the
	// decode refusal this bound exists to prevent.
	per := l.nCtx / l.batchDocs
	per -= per % localSeqTokenBlock
	return min(max(per, localSeqTokenBlock), l.nCtx)
}

// truncateTokens clamps tokens to nCtx, keeping EOS as the final token —
// with last-token pooling the embedding is read from the final position,
// which must stay the trained pooling token after truncation.
func truncateTokens(tokens []llama.Token, nCtx int, eos llama.Token) []llama.Token {
	if len(tokens) <= nCtx {
		return tokens
	}
	out := make([]llama.Token, nCtx)
	copy(out, tokens[:nCtx-1])
	out[nCtx-1] = eos
	return out
}

func l2Normalize(vec []float32) {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return
	}
	inv := float32(1 / math.Sqrt(sum))
	for i := range vec {
		vec[i] *= inv
	}
}

// embedOne runs one text through the context. Caller holds l.mu and has
// run ensureLoaded.
func (l *Local) embedOne(text string) ([]float32, error) {
	vecs, err := l.embedGroup([][]llama.Token{l.tokenize(text)})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

// tokenize prepares one text for decoding: never empty, truncated to
// nCtx with EOS kept terminal (last-token pooling). Caller holds l.mu.
func (l *Local) tokenize(text string) []llama.Token {
	eos := llama.VocabEOS(l.vocab)
	tokens := llama.Tokenize(l.vocab, text, true, true)
	if len(tokens) == 0 {
		tokens = []llama.Token{eos}
	}
	return truncateTokens(tokens, l.maxDocTokens(), eos)
}

// groupEnd returns the exclusive end index of the next decode group
// starting at `start`: greedy in order, bounded by `budget` total tokens
// and `maxSeqs` sequences. Always advances by at least one (every doc is
// pre-truncated to the budget).
func groupEnd(lens []int, start, budget, maxSeqs int) int {
	end := start
	for end < len(lens) && end-start < maxSeqs && lens[end] <= budget {
		budget -= lens[end]
		end++
	}
	return max(end, start+1)
}

// embedGroup packs the pre-tokenized docs into one llama_decode as
// parallel sequences and reads one pooled embedding per sequence.
// len(tokenized) must respect groupEnd's bounds. Caller holds l.mu.
func (l *Local) embedGroup(tokenized [][]llama.Token) ([][]float32, error) {
	total := 0
	for _, tokens := range tokenized {
		total += len(tokens)
	}

	batch := llama.BatchInit(int32(total), 0, 1)
	defer llama.BatchFree(batch)
	for seq, tokens := range tokenized {
		for pos, tok := range tokens {
			batch.Add(tok, llama.Pos(pos), []llama.SeqId{llama.SeqId(seq)}, true)
		}
	}

	// Sequences are reused across decodes — drop the previous KV state.
	if mem, err := llama.GetMemory(l.lctx); err == nil {
		_ = llama.MemoryClear(mem, true)
	}
	ret, err := llama.Decode(l.lctx, batch)
	if err != nil {
		return nil, fmt.Errorf("indexer: local embedder: decode: %w", err)
	}
	if ret != 0 {
		return nil, fmt.Errorf("indexer: local embedder: decode returned %d", ret)
	}

	out := make([][]float32, len(tokenized))
	for seq := range tokenized {
		raw, err := llama.GetEmbeddingsSeq(l.lctx, llama.SeqId(seq), l.nEmbd)
		if err != nil {
			return nil, fmt.Errorf("indexer: local embedder: get embeddings: %w", err)
		}
		if raw == nil {
			return nil, fmt.Errorf("indexer: local embedder: no embeddings returned")
		}
		// raw views llama-owned memory — copy before the next decode.
		vec := make([]float32, len(raw))
		copy(vec, raw)
		l2Normalize(vec)
		if l.outDim > 0 && l.outDim < len(vec) {
			vec = vec[:l.outDim] // Matryoshka truncation
			l2Normalize(vec)
		}
		out[seq] = vec
	}
	return out, nil
}

func (l *Local) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureLoaded(); err != nil {
		return nil, err
	}
	tokenized := make([][]llama.Token, len(texts))
	lens := make([]int, len(texts))
	for i, t := range texts {
		tokenized[i] = l.tokenize(t)
		lens[i] = len(tokenized[i])
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := groupEnd(lens, start, l.nCtx, l.batchDocs)
		vecs, err := l.embedGroup(tokenized[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
		start = end
	}
	return out, nil
}

func (l *Local) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureLoaded(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.embedOne(l.queryPrefix + text)
}

// Dim avoids loading the model for the pinned default (the store learns
// the dimension from the first embedded batch anyway); a custom model
// must be loaded to know.
func (l *Local) Dim(ctx context.Context) (int, error) {
	if l.defaultModel {
		return min(orDefault(l.outDim, localDefaultDim), localDefaultDim), nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureLoaded(); err != nil {
		return 0, err
	}
	return min(orDefault(l.outDim, int(l.nEmbd)), int(l.nEmbd)), nil
}

func orDefault(v, def int) int {
	if v > 0 {
		return v
	}
	return def
}

// Close stops the background download and frees llama resources. The
// process-global backend state stays loaded (other instances may use it).
func (l *Local) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dl != nil {
		l.dl.Close()
	}
	if l.loaded {
		_ = llama.Free(l.lctx)
		_ = llama.ModelFree(l.model)
		l.loaded = false
	}
	l.closed = true
	return nil
}
