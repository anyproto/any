package indexer

import (
	"context"
	"io"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"
)

// fallbackEmbedder prefers a primary embedder (typically a fast online
// API) and falls back to a secondary (the in-process local model) when
// the primary errors. A circuit breaker avoids paying the primary's
// network timeout on every call during a sustained outage: after
// failThreshold consecutive failures it skips the primary for a cooldown,
// then probes once (half-open) and closes again on success.
//
// CRITICAL: primary and fallback MUST be the SAME embedding model. The
// index stores one set of doc vectors and one dim; mixing models (or
// dims) produces incoherent similarity. The supported pairing is the same
// model served two ways — e.g. Qwen3-Embedding-0.6B online (fp16) and
// local (Q8); the quantization drift is negligible.
type fallbackEmbedder struct {
	primary  Embedder
	fallback Embedder
	lg       logger.CtxLogger

	failThreshold int
	cooldown      time.Duration
	now           func() time.Time

	mu          sync.Mutex
	consecFails int
	openUntil   time.Time // primary skipped while now < openUntil
}

func newFallbackEmbedder(primary, fallback Embedder) *fallbackEmbedder {
	return &fallbackEmbedder{
		primary:       primary,
		fallback:      fallback,
		lg:            logger.NewNamed("indexer.embedder"),
		failThreshold: 3,
		cooldown:      30 * time.Second,
		now:           time.Now,
	}
}

// allowPrimary reports whether the breaker permits a primary attempt
// (closed, or half-open after the cooldown).
func (f *fallbackEmbedder) allowPrimary() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.now().Before(f.openUntil)
}

func (f *fallbackEmbedder) recordOK() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.consecFails >= f.failThreshold {
		f.lg.Info("primary embedder recovered; resuming online embedding")
	}
	f.consecFails = 0
	f.openUntil = time.Time{}
}

func (f *fallbackEmbedder) recordFail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consecFails++
	if f.consecFails == f.failThreshold { // log once per outage episode
		f.lg.Warn("primary embedder down; falling back to local",
			zap.Int("consecutiveFailures", f.consecFails), zap.Error(err))
	}
	if f.consecFails >= f.failThreshold {
		f.openUntil = f.now().Add(f.cooldown)
	}
}

func (f *fallbackEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	if f.allowPrimary() {
		if v, err := f.primary.EmbedDocs(ctx, texts); err == nil {
			f.recordOK()
			return v, nil
		} else {
			f.recordFail(err)
		}
	}
	return f.fallback.EmbedDocs(ctx, texts)
}

func (f *fallbackEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if f.allowPrimary() {
		if v, err := f.primary.EmbedQuery(ctx, text); err == nil {
			f.recordOK()
			return v, nil
		} else {
			f.recordFail(err)
		}
	}
	return f.fallback.EmbedQuery(ctx, text)
}

// Dim returns the fallback (local) dimension — known offline without a
// network probe, and equal to the primary's by the same-model contract.
func (f *fallbackEmbedder) Dim(ctx context.Context) (int, error) {
	return f.fallback.Dim(ctx)
}

// SetThreads forwards the CPU budget to the fallback — the local model
// is the only leg that decodes on this machine.
func (f *fallbackEmbedder) SetThreads(n int) {
	if s, ok := f.fallback.(interface{ SetThreads(int) }); ok {
		s.SetThreads(n)
	}
}

// Hardware reports the fallback's machine — the local model is the only
// leg that runs here, and under the default `auto` embedder this
// wrapper is what Indexer.EmbedHardware sees.
func (f *fallbackEmbedder) Hardware() Hardware {
	if h, ok := f.fallback.(interface{ Hardware() Hardware }); ok {
		return h.Hardware()
	}
	return Hardware{}
}

// Close releases the fallback's resources (the local model owns the
// background download + llama runtime). The primary (HTTP client) has none.
func (f *fallbackEmbedder) Close() error {
	if c, ok := f.fallback.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
