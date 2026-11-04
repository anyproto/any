package indexer

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sync"
	"testing"
)

// vecCacheEmbedder persists doc and query vectors in an append-only file
// keyed by text, so a re-run (another dim, other knobs) skips the
// embedder. One file per model configuration: the key is the raw text,
// before any prefix the embedder adds.
type vecCacheEmbedder struct {
	inner   Embedder
	mu      sync.Mutex
	f       *os.File
	w       *bufio.Writer
	docs    map[string][]float32
	queries map[string][]float32
	dim     int
}

const (
	vecCacheDoc   byte = 'd'
	vecCacheQuery byte = 'q'
)

func openVecCache(t *testing.T, path string, inner Embedder) *vecCacheEmbedder {
	t.Helper()
	c := &vecCacheEmbedder{inner: inner, docs: map[string][]float32{}, queries: map[string][]float32{}}
	if f, err := os.Open(path); err == nil {
		r := bufio.NewReaderSize(f, 1<<20)
		for {
			kind, key, vec, err := readVecRecord(r)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.Logf("vec cache %s: stopping at a torn record: %v", path, err)
				}
				break
			}
			if kind == vecCacheDoc {
				c.docs[key] = vec
			} else {
				c.queries[key] = vec
			}
			c.dim = len(vec)
		}
		f.Close()
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	c.f, c.w = f, bufio.NewWriterSize(f, 1<<20)
	t.Cleanup(func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		_ = c.w.Flush()
		_ = c.f.Close()
	})
	t.Logf("vec cache %s: %d docs, %d queries, dim %d", path, len(c.docs), len(c.queries), c.dim)
	return c
}

// readVecRecord reads kind, key length, key, vector length, floats.
func readVecRecord(r *bufio.Reader) (byte, string, []float32, error) {
	kind, err := r.ReadByte()
	if err != nil {
		return 0, "", nil, err
	}
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return 0, "", nil, io.ErrUnexpectedEOF
	}
	key := make([]byte, n)
	if _, err := io.ReadFull(r, key); err != nil {
		return 0, "", nil, io.ErrUnexpectedEOF
	}
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return 0, "", nil, io.ErrUnexpectedEOF
	}
	vec := make([]float32, n)
	if err := binary.Read(r, binary.LittleEndian, vec); err != nil {
		return 0, "", nil, io.ErrUnexpectedEOF
	}
	return kind, string(key), vec, nil
}

func (c *vecCacheEmbedder) put(kind byte, key string, vec []float32) {
	_ = c.w.WriteByte(kind)
	_ = binary.Write(c.w, binary.LittleEndian, uint32(len(key)))
	_, _ = c.w.WriteString(key)
	_ = binary.Write(c.w, binary.LittleEndian, uint32(len(vec)))
	_ = binary.Write(c.w, binary.LittleEndian, vec)
	c.dim = len(vec)
}

func (c *vecCacheEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	c.mu.Lock()
	out := make([][]float32, len(texts))
	var miss []int
	for i, s := range texts {
		if v, ok := c.docs[s]; ok {
			out[i] = v
		} else {
			miss = append(miss, i)
		}
	}
	c.mu.Unlock()
	if len(miss) == 0 {
		return out, nil
	}
	in := make([]string, len(miss))
	for j, i := range miss {
		in[j] = texts[i]
	}
	got, err := c.inner.EmbedDocs(ctx, in)
	c.mu.Lock()
	defer c.mu.Unlock()
	for j, v := range got {
		if v == nil {
			continue
		}
		out[miss[j]] = v
		c.docs[in[j]] = v
		c.put(vecCacheDoc, in[j], v)
	}
	_ = c.w.Flush()
	if err != nil {
		// Keep the leading-prefix contract: return what is contiguous.
		for i, v := range out {
			if v == nil {
				return out[:i], err
			}
		}
	}
	return out, err
}

func (c *vecCacheEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	c.mu.Lock()
	if v, ok := c.queries[text]; ok {
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	v, err := c.inner.EmbedQuery(ctx, text)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queries[text] = v
	c.put(vecCacheQuery, text, v)
	_ = c.w.Flush()
	return v, nil
}

func (c *vecCacheEmbedder) Dim(ctx context.Context) (int, error) {
	c.mu.Lock()
	d := c.dim
	c.mu.Unlock()
	if d > 0 {
		return d, nil
	}
	return c.inner.Dim(ctx)
}

// truncEmbedder applies Matryoshka truncation and renormalization, as the
// local embedder does for index.local.dim.
type truncEmbedder struct {
	inner Embedder
	dim   int
}

func truncVec(v []float32, dim int) []float32 {
	if dim <= 0 || dim >= len(v) {
		return v
	}
	out := make([]float32, dim)
	copy(out, v[:dim])
	var s float64
	for _, x := range out {
		s += float64(x) * float64(x)
	}
	if s > 0 {
		inv := float32(1 / math.Sqrt(s))
		for i := range out {
			out[i] *= inv
		}
	}
	return out
}

func (e truncEmbedder) EmbedDocs(ctx context.Context, texts []string) ([][]float32, error) {
	vs, err := e.inner.EmbedDocs(ctx, texts)
	for i := range vs {
		vs[i] = truncVec(vs[i], e.dim)
	}
	return vs, err
}

func (e truncEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := e.inner.EmbedQuery(ctx, text)
	return truncVec(v, e.dim), err
}

func (e truncEmbedder) Dim(ctx context.Context) (int, error) {
	d, err := e.inner.Dim(ctx)
	return min(d, e.dim), err
}
