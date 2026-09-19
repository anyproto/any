//go:build llamacpp && !android && !ios

package indexer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/anyproto/any/internal/config"
)

// EmbedWorkerConfig is the child's whole input — the parent renders it
// into flags (see internal/cli, `any run embedder`). The model path is
// always explicit: the parent owns discovery and the background
// download, so the child never fetches anything.
type EmbedWorkerConfig struct {
	ModelPath   string
	LibDir      string
	QueryPrefix string
	ContextSize int
	Threads     int
	Dim         int
	BatchDocs   int
	// GpuLayers overrides llama.cpp's n_gpu_layers; negative keeps the
	// llama.cpp default (offload everything when a GPU is present).
	GpuLayers int
	// Niceness lowers this process's scheduling priority; 0 = leave it.
	Niceness int
}

// RunEmbedWorker serves embed requests over the frame protocol until in
// reaches EOF (the parent closed the pipe, or died) or a close request
// arrives. It loads the model up front and answers with `ready` so the
// parent learns the dimension and knows the child is usable; a load
// failure exits before the handshake, and the parent reports it with
// the child's stderr tail.
//
// This is the process boundary that keeps a llama.cpp abort — a Vulkan
// device-lost throwing through the FFI frame, a GGML_ASSERT — from
// taking the server down with it (docs/13-index.md § GPU offload).
func RunEmbedWorker(ctx context.Context, cfg EmbedWorkerConfig, in io.Reader, out io.Writer) error {
	if cfg.ModelPath == "" {
		return errors.New("indexer: embed worker: --model is required")
	}
	// Before the model loads, so llama.cpp's decode threads inherit it.
	lowerChildPriority(cfg.Niceness)
	local := config.IndexLocal{
		ModelPath:   cfg.ModelPath,
		LibDir:      cfg.LibDir,
		QueryPrefix: cfg.QueryPrefix,
		ContextSize: cfg.ContextSize,
		Threads:     cfg.Threads,
		Dim:         cfg.Dim,
		BatchDocs:   cfg.BatchDocs,
	}
	if cfg.GpuLayers >= 0 {
		local.GpuLayers = &cfg.GpuLayers
	}
	l, err := NewLocal(local, "", "", nil)
	if err != nil {
		return err
	}
	defer l.Close()

	// Dim loads the model (ModelPath is set, so this is never the
	// default-model shortcut) — the handshake means "ready to decode".
	dim, err := l.Dim(ctx)
	if err != nil {
		return err
	}

	r := bufio.NewReaderSize(in, 64<<10)
	w := bufio.NewWriter(out)
	hw := l.Hardware()
	if err := writeFrame(w, workerResp{Op: workerOpReady, Dim: dim, Hardware: &hw}, nil); err != nil {
		return err
	}

	// Reading runs on its own goroutine so the end of the stream — the
	// parent closing stdin, or dying — is noticed even mid-decode. A
	// decode cannot be interrupted, so a wedged child that waited for
	// the next request to spot the EOF would outlive its parent holding
	// the model; it exits instead.
	var decoding atomic.Bool
	reqs := make(chan workerReq)
	readErr := make(chan error, 1)
	go func() {
		defer close(reqs)
		for {
			var req workerReq
			if _, err := readFrame(r, &req); err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
					readErr <- err
				}
				if decoding.Load() {
					os.Exit(0)
				}
				return
			}
			reqs <- req
		}
	}()

	for req := range reqs {
		switch req.Op {
		case workerOpClose:
			return nil
		case workerOpDim:
			if err := writeFrame(w, workerResp{Op: workerOpDim, Dim: dim}, nil); err != nil {
				return err
			}
		case workerOpEmbed:
			decoding.Store(true)
			err := serveEmbed(ctx, l, w, req)
			decoding.Store(false)
			if err != nil {
				return err
			}
		default:
			if err := writeFrame(w, workerResp{Op: workerOpError, Error: fmt.Sprintf("unknown op %q", req.Op)}, nil); err != nil {
				return err
			}
		}
	}
	select {
	case err := <-readErr:
		return err
	default:
		return nil // parent gone
	}
}

// serveEmbed answers one embed request. An embedding failure is an
// error FRAME, not a transport error: the child stays usable and the
// parent keeps it (only a dead or desynchronized child is respawned).
func serveEmbed(ctx context.Context, l *Local, w *bufio.Writer, req workerReq) error {
	var (
		vecs [][]float32
		err  error
	)
	switch req.Role {
	case workerRoleQuery:
		if len(req.Texts) != 1 {
			return writeFrame(w, workerResp{Op: workerOpError,
				Error: fmt.Sprintf("query role takes exactly one text, got %d", len(req.Texts))}, nil)
		}
		var v []float32
		if v, err = l.EmbedQuery(ctx, req.Texts[0]); err == nil {
			vecs = [][]float32{v}
		}
	default:
		vecs, err = l.EmbedDocs(ctx, req.Texts)
	}
	if err != nil {
		return writeFrame(w, workerResp{Op: workerOpError, Error: err.Error()}, nil)
	}
	bin, dim, err := encodeVectors(vecs)
	if err != nil {
		return writeFrame(w, workerResp{Op: workerOpError, Error: err.Error()}, nil)
	}
	return writeFrame(w, workerResp{Op: workerOpVectors, N: len(vecs), Dim: dim}, bin)
}
