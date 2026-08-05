//go:build vector && !gomobile && !mobile

package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"go.uber.org/zap"
)

// modelDownload fetches a GGUF model into the data dir in the
// background. It never blocks server boot: the local embedder stats the
// destination file on every embed attempt, so the existing pending/
// retry semantics in the embed loop turn "still downloading" into an
// ordinary embedder outage that heals itself when the file lands.
//
// The body streams to <dest>.part with the sha256 computed on the way;
// on success the file is atomically renamed into place. Interrupted
// downloads resume via a Range request (re-hashing the existing .part
// prefix first). Failures retry forever with capped backoff — the
// destination file is the readiness signal, not this object.
type modelDownload struct {
	url    string
	dest   string
	sha    string // expected hex sha256; "" = skip verification (logged once)
	lg     logger.CtxLogger
	cancel context.CancelFunc

	mu    sync.Mutex
	done  bool
	err   error // last failure, surfaced by Status between retries
	got   int64
	total int64
}

// startModelDownload spawns the download goroutine. Pass client=nil for
// the default HTTP client (tests inject their own).
func startModelDownload(url, dest, sha string, client *http.Client) *modelDownload {
	ctx, cancel := context.WithCancel(context.Background())
	d := &modelDownload{
		url:    url,
		dest:   dest,
		sha:    sha,
		lg:     logger.NewNamed("indexer"),
		cancel: cancel,
	}
	if client == nil {
		client = &http.Client{} // no timeout: a 640MB body on slow links is legitimate
	}
	if sha == "" {
		d.lg.Warn("local embedder: no sha256 pinned for model download, skipping verification", zap.String("url", url))
	}
	go d.run(ctx, client)
	return d
}

// Status reports the current state as an error: nil when the file is in
// place, a progress pseudo-error while downloading, or the last failure.
func (d *modelDownload) Status() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.done {
		return nil
	}
	if d.err != nil {
		return fmt.Errorf("model download failed (retrying): %w", d.err)
	}
	if d.total > 0 {
		return fmt.Errorf("downloading model: %d%% (%d/%d MB)",
			d.got*100/d.total, d.got>>20, d.total>>20)
	}
	return fmt.Errorf("downloading model: %d MB so far", d.got>>20)
}

// Close stops the download goroutine. The .part file stays for resume.
func (d *modelDownload) Close() {
	d.cancel()
}

func (d *modelDownload) run(ctx context.Context, client *http.Client) {
	backoff := 5 * time.Second
	for {
		err := d.attempt(ctx, client)
		if err == nil {
			d.mu.Lock()
			d.done = true
			d.mu.Unlock()
			d.lg.Info("local embedder: model downloaded", zap.String("dest", d.dest))
			return
		}
		if ctx.Err() != nil {
			return
		}
		d.mu.Lock()
		d.err = err
		d.mu.Unlock()
		d.lg.Warn("local embedder: model download failed, will retry",
			zap.Duration("backoff", backoff), zap.Error(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
	}
}

func (d *modelDownload) attempt(ctx context.Context, client *http.Client) error {
	if err := os.MkdirAll(filepath.Dir(d.dest), 0o755); err != nil {
		return err
	}
	part := d.dest + ".part"

	// Resume: re-hash whatever is already on disk so the streaming
	// checksum stays valid, then ask for the remainder.
	h := sha256.New()
	var offset int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 {
		if err := hashFilePrefix(part, st.Size(), h); err == nil {
			offset = st.Size()
		} else {
			_ = os.Remove(part)
			h = sha256.New()
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case offset > 0 && resp.StatusCode == http.StatusPartialContent:
		// resuming where we left off
	case resp.StatusCode == http.StatusOK:
		// full body (server ignored the Range, or fresh start)
		if offset > 0 {
			h = sha256.New()
		}
		offset = 0
	default:
		return fmt.Errorf("GET %s: %s", d.url, resp.Status)
	}

	total := offset + resp.ContentLength
	if resp.ContentLength < 0 {
		total = 0
	}
	d.mu.Lock()
	d.got, d.total, d.err = offset, total, nil
	d.mu.Unlock()

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	if err := d.stream(ctx, f, resp.Body, h, offset); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}

	if d.sha != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != d.sha {
			_ = os.Remove(part)
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, d.sha)
		}
	}
	return os.Rename(part, d.dest)
}

// stream copies body → file + hash, updating progress and logging it
// every ~5s.
func (d *modelDownload) stream(ctx context.Context, f *os.File, body io.Reader, h hash.Hash, got int64) error {
	w := io.MultiWriter(f, h)
	buf := make([]byte, 1<<20)
	lastLog := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			got += int64(n)
			d.mu.Lock()
			d.got = got
			total := d.total
			d.mu.Unlock()
			if time.Since(lastLog) > 5*time.Second {
				lastLog = time.Now()
				if total > 0 {
					d.lg.Info("local embedder: downloading model",
						zap.Int64("percent", got*100/total),
						zap.Int64("gotMB", got>>20), zap.Int64("totalMB", total>>20))
				} else {
					d.lg.Info("local embedder: downloading model", zap.Int64("gotMB", got>>20))
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func hashFilePrefix(path string, n int64, h hash.Hash) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.CopyN(h, f, n)
	return err
}
