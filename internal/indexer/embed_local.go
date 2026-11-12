//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/hybridgroup/yzma/pkg/llama"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
)

// Zero-config defaults: `index.embedder: local` alone downloads the
// default model (embed_model.go) into the models dir and embeds with it.
const (
	// Truncation bound, not the model maximum: for
	// embeddings NBatch must cover the whole input, so context size is
	// also the compute/memory bound per text. The bound is per SEQUENCE
	// (maxDocTokens) — batchDocs > 1 divides it. docs/13-index.md
	// § Known limits records the truncation decision.
	localDefaultCtx = 2048
	// Docs packed per llama_decode as parallel sequences. The unified KV
	// cache PARTITIONS the context across NSeqMax, so packing N docs cuts
	// the per-doc token bound to nCtx/N — a wider batch buys throughput
	// only by truncating every text. One doc per decode keeps the whole
	// nCtx available to it, which is what localDefaultCtx documents.
	localDefaultBatchDocs = 1
	// Background niceness for the embedder child. Indexing is never
	// urgent: it must not compete with what the user is doing.
	localDefaultNiceness = 10
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
	modelPath   string
	libDir      string
	nCtx        int
	threads     int // 0 = runtime.NumCPU()-1
	outDim      int // 0 = model dim; >0 = Matryoshka truncate + renormalize
	queryPrefix string
	docPrefix   string
	pooling     llama.PoolingType
	project     []float32 // unit direction removed from every vector; nil = none
	gpuLayers   int       // -1 = llama.cpp default (offload all when a GPU is present)
	batchDocs   int       // max docs packed per llama_decode (≥1)
	modelDim    int       // output width known without loading; 0 = load to learn it
	wantDim     int       // the pinned model's output width; 0 = an unnamed GGUF
	stampId     string
	stampLegacy bool

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
	spec, dl, err := resolveLocalSpec(cfg, modelsDir, legacyModelsDir, onProcess)
	if err != nil {
		return nil, err
	}
	return newLocalFromSpec(spec, dl), nil
}

// localSpec is everything needed to run the model: resolved paths plus
// decode params. Shared by the in-process Local (which the embedder
// child runs) and the parent-side supervisor, which renders it into the
// child's flags — so both sides derive their parameters from one place.
type localSpec struct {
	modelPath   string
	libDir      string
	profile     string // pinned model id; "" = an unnamed GGUF
	queryPrefix string
	docPrefix   string
	pooling     string // poolingType name
	nCtx        int
	threads     int // 0 = runtime.NumCPU()-1
	outDim      int
	batchDocs   int
	gpuLayers   int // -1 = llama.cpp default (offload all when a GPU is present)
	niceness    int // child scheduling priority; 0 = same as the server
	modelDim    int // pinned model at its default path: width known without loading
	wantDim     int // the pinned model's output width; 0 = an unnamed GGUF
}

// resolveLocalSpec turns config into a runnable spec: the model and its
// path (incl. the legacy per-data-dir location), lib dir, prompts,
// pooling and decode bounds. A ModelPath with no
// index.local.model is an unnamed GGUF: it takes no pinned model's
// prompts or pooling, only what the config sets. A missing default model starts the background download, whose
// handle it returns — readiness is the destination file, so callers keep
// treating "still downloading" as an ordinary embedder outage.
func resolveLocalSpec(cfg config.IndexLocal, modelsDir, legacyModelsDir string, onProcess func(ProcessUpdate)) (localSpec, *modelDownload, error) {
	s := localSpec{
		nCtx:        cfg.ContextSize,
		threads:     cfg.Threads,
		outDim:      cfg.Dim,
		queryPrefix: cfg.QueryPrefix,
		docPrefix:   cfg.DocPrefix,
		pooling:     cfg.Pooling,
		gpuLayers:   -1,
		niceness:    localDefaultNiceness,
		batchDocs:   cfg.BatchDocs,
	}
	// A ModelUrl alone is another source for the default model.
	named := cfg.Model != "" || cfg.ModelPath == ""
	model, err := lookupLocalModel(cfg.Model)
	if err != nil {
		return localSpec{}, nil, err
	}
	if cfg.Niceness != nil {
		s.niceness = max(*cfg.Niceness, 0)
	}
	if s.nCtx <= 0 {
		s.nCtx = localDefaultCtx
	}
	if cfg.GpuLayers != nil {
		s.gpuLayers = *cfg.GpuLayers
	}
	if s.batchDocs <= 0 {
		s.batchDocs = localDefaultBatchDocs
	}
	if per := maxDocTokens(s.nCtx, s.batchDocs); per < s.nCtx {
		logger.NewNamed("indexer").Warn("index: local embedder batchDocs truncates texts below contextSize",
			zap.Int("batchDocs", s.batchDocs), zap.Int("contextSize", s.nCtx), zap.Int("maxDocTokens", per))
	}

	s.libDir = cfg.LibDir
	if s.libDir == "" {
		s.libDir = os.Getenv("YZMA_LIB")
	}
	if s.libDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return localSpec{}, nil, fmt.Errorf("indexer: local embedder: resolve executable for default libDir: %w", err)
		}
		s.libDir = filepath.Join(filepath.Dir(exe), "llamacpp")
	}

	if named {
		s.profile = model.id
		s.wantDim = model.dim
		if s.queryPrefix == "" {
			s.queryPrefix = model.queryPrefix
		}
		if s.docPrefix == "" {
			s.docPrefix = model.docPrefix
		}
		if s.pooling == "" {
			s.pooling = model.pooling
		}
	}
	// Every config check precedes the download: a refused spec must not
	// leave one running.
	if _, err := poolingType(s.pooling); err != nil {
		return localSpec{}, nil, err
	}

	var dl *modelDownload
	if cfg.ModelPath != "" {
		// Air-gapped: the file is the user's responsibility, no download.
		s.modelPath = cfg.ModelPath
	} else {
		s.modelPath = filepath.Join(modelsDir, model.file)
		if legacy := filepath.Join(legacyModelsDir, model.file); legacyModelsDir != "" {
			if _, err := os.Stat(legacy); err == nil {
				s.modelPath = legacy
			}
		}
		if named {
			s.modelDim = model.dim
		}
		if _, err := os.Stat(s.modelPath); err != nil {
			url, sha := model.url, model.sha256
			// A mirror serves the pinned file, so the pinned checksum holds.
			if cfg.ModelUrl != "" {
				url = cfg.ModelUrl
			}
			if cfg.ModelSha256 != "" {
				sha = cfg.ModelSha256
			}
			dl = startModelDownload(url, s.modelPath, sha, nil, onProcess)
		}
	}
	return s, dl, nil
}

func newLocalFromSpec(s localSpec, dl *modelDownload) *Local {
	pooling, _ := poolingType(s.pooling) // validated by resolveLocalSpec
	stampId, stampLegacy := s.stamp()
	var project []float32
	if s.profile != "" {
		if m, err := lookupLocalModel(s.profile); err == nil {
			project = m.project
		}
	}
	return &Local{
		modelPath:   s.modelPath,
		libDir:      s.libDir,
		nCtx:        s.nCtx,
		threads:     s.threads,
		outDim:      s.outDim,
		queryPrefix: s.queryPrefix,
		docPrefix:   s.docPrefix,
		pooling:     pooling,
		project:     project,
		gpuLayers:   s.gpuLayers,
		batchDocs:   s.batchDocs,
		modelDim:    s.modelDim,
		wantDim:     s.wantDim,
		stampId:     stampId,
		stampLegacy: stampLegacy,
		dl:          dl,
	}
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
		return fmt.Errorf("indexer: local embedder: llama.cpp %s libs not found at %s (run 'make llamacpp', or set index.local.libDir to that release's libs): %w", llamaCppRelease, libDir, err)
	}
	if err := addLibSearchDir(libDir); err != nil {
		return fmt.Errorf("indexer: local embedder: add %s to the library search path: %w", libDir, err)
	}
	if err := llama.Load(libDir); err != nil {
		return fmt.Errorf("indexer: local embedder: load llama.cpp from %s: %w", libDir, err)
	}
	// Silent except for the handful of lines naming the backends that
	// loaded and the devices they found (Hardware).
	llama.LogSet(llamaLogCapture())
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
	cp.PoolingType = l.pooling
	cp.Embeddings = 1
	cp.OpOffload = opOffload(l.gpuLayers)
	lctx, err := llama.InitFromModel(model, cp)
	if err != nil {
		_ = llama.ModelFree(model)
		return fmt.Errorf("indexer: local embedder: init context: %w", err)
	}

	// The pooled vector has the output width, which a model with an
	// output projection has wider than its hidden width.
	nEmbd := llama.ModelNEmbdOut(model)
	// A file that is not the model it stands for (a mirror of another
	// one) would embed under the wrong prompts into the wrong stamp.
	if l.wantDim > 0 && int(nEmbd) != l.wantDim {
		_ = llama.Free(lctx)
		_ = llama.ModelFree(model)
		return fmt.Errorf("indexer: local embedder: %s is not the configured model: output width %d, want %d — set index.local.model to the model the file is", l.modelPath, nEmbd, l.wantDim)
	}
	l.model, l.lctx = model, lctx
	l.vocab = llama.ModelGetVocab(model)
	l.nEmbd = nEmbd
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
// opOffload maps the gpu-layer setting onto llama.cpp's op_offload.
// n_gpu_layers only decides where the WEIGHTS live; with op_offload on
// (the default) llama.cpp still hands whole matmuls to a registered GPU
// device, so `gpuLayers: 0` alone leaves the compute on the GPU.
// Measured on an RDNA2 iGPU with a 1200-token document: 6.6 s with op
// offload against 1.1 s across 16 CPU threads without it — and the
// offloaded path is the one that hangs the compute ring. CPU-only has
// to mean both.
func opOffload(gpuLayers int) uint8 {
	if gpuLayers == 0 {
		return 0
	}
	return 1
}

func (l *Local) maxDocTokens() int { return maxDocTokens(l.nCtx, l.batchDocs) }

func maxDocTokens(nCtx, batchDocs int) int {
	if batchDocs <= 1 {
		return nCtx // one sequence, the whole context is its slot
	}
	// Floor to a whole block: rounding up would promise more than
	// nCtx/batchDocs when batchDocs does not divide nCtx, which is the
	// decode refusal this bound exists to prevent.
	per := nCtx / batchDocs
	per -= per % localSeqTokenBlock
	return min(max(per, localSeqTokenBlock), nCtx)
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
	// Metadata only: zeroing the buffers memsets the whole cache per call.
	if mem, err := llama.GetMemory(l.lctx); err == nil {
		_ = llama.MemoryClear(mem, false)
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
		if l.project != nil {
			projectOut(vec, l.project) // at full width, before any truncation
		}
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
	return l.EmbedTitledDocs(ctx, nil, texts)
}

func (l *Local) Stamp() (string, bool) { return l.stampId, l.stampLegacy }

// EmbedsTitles: the document prompt has a title slot.
func (l *Local) EmbedsTitles() bool {
	return strings.Contains(l.docPrefix, titlePlaceholder)
}

func (l *Local) EmbedTitledDocs(ctx context.Context, titles, texts []string) ([][]float32, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.ensureLoaded(); err != nil {
		return nil, err
	}
	tokenized := make([][]llama.Token, len(texts))
	lens := make([]int, len(texts))
	for i, t := range texts {
		title := ""
		if i < len(titles) {
			title = titles[i]
		}
		tokenized[i] = l.tokenize(docPrompt(l.docPrefix, title) + t)
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

// Dim avoids loading a pinned model at its default path (the store
// learns the dimension from the first embedded batch anyway); any other
// file must be loaded to know.
func (l *Local) Dim(ctx context.Context) (int, error) {
	if l.modelDim > 0 {
		return min(orDefault(l.outDim, l.modelDim), l.modelDim), nil
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
