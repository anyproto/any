//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	"github.com/gofrs/flock"
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
// prefix first). Failures retry with capped backoff — the destination
// file is the readiness signal, not this object — until the source
// fails the checksum twice: it serves another file, and fetching it
// again cannot help.
//
// The models dir is shared by every account under one root, so the
// .part is guarded across processes by an OS lock on <dest>.lock: one
// process downloads, the others poll for the installed file.
type modelDownload struct {
	url    string
	dest   string
	sha    string // expected hex sha256; "" = skip verification (logged once)
	lg     logger.CtxLogger
	cancel context.CancelFunc
	// finished closes when run returns; Close joins on it, so a closed
	// download no longer writes the .part — the models dir outlives an
	// engine and the next one resumes the same file.
	finished chan struct{}
	// rep reports the download onto the process view
	// (ProcessKindModelDownload; Done/Total = bytes). Immediate gate:
	// a download is long by definition and its absence — offline
	// machine included — is exactly the state worth showing; the
	// reporter's heartbeat keeps the row alive through retry backoffs.
	rep *procReporter
	// lock is the cross-process download lock on <dest>.lock, one handle
	// for the process: after a failed unlock its next TryLock finds the
	// lock still held here instead of waiting on itself.
	lock *flock.Flock

	mu    sync.Mutex
	done  bool
	err   error // last failure, surfaced by Status between retries
	got   int64
	total int64
}

// startModelDownload spawns the download goroutine. Pass client=nil for
// the default HTTP client (tests inject their own).
func startModelDownload(url, dest, sha string, client *http.Client, onProcess func(ProcessUpdate)) *modelDownload {
	ctx, cancel := context.WithCancel(context.Background())
	d := &modelDownload{
		url:      url,
		dest:     dest,
		sha:      sha,
		lg:       logger.NewNamed("indexer"),
		cancel:   cancel,
		finished: make(chan struct{}),
		rep: newProcReporter(onProcess,
			ProcessUpdate{Kind: ProcessKindModelDownload, Name: filepath.Base(dest)}, -1),
		lock: flock.New(dest + ".lock"),
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
	if errors.Is(d.err, errDownloadElsewhere) || errors.Is(d.err, errDownloadStopped) {
		return d.err
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

// Close stops the download goroutine and waits for it. The .part file
// stays for resume.
func (d *modelDownload) Close() {
	d.cancel()
	<-d.finished
}

// errDownloadElsewhere is an attempt that found another process
// holding the download lock — a wait, not a failure.
var errDownloadElsewhere = errors.New("model download in progress in another process")

// downloadPollInterval is how often a process that lost the download
// lock looks for the installed file (a var so tests can shorten it).
var downloadPollInterval = 5 * time.Second

// downloadRetryBackoff is the first wait after a failed attempt; it
// doubles up to 5 minutes (a var so tests can shorten it).
var downloadRetryBackoff = 5 * time.Second

// errChecksumMismatch is a fully fetched file that is not the expected one.
var errChecksumMismatch = errors.New("sha256 mismatch")

// errDownloadStopped ends the retries until the next start: the source
// served a file that failed the checksum twice.
var errDownloadStopped = errors.New("model download stopped: the source serves a file that is not the expected model; correct index.local.modelUrl or modelSha256 and restart")

// errDownloadStalled is a transfer the stall watchdog cut.
var errDownloadStalled = errors.New("model download stalled")

// downloadStallTimeout is how long a transfer may send nothing before it
// is cut and retried: it holds the lock every process under the root
// waits on (a var so tests can shorten it).
var downloadStallTimeout = 2 * time.Minute

func (d *modelDownload) run(ctx context.Context, client *http.Client) {
	defer close(d.finished)
	d.rep.begin() // announce up front — "still fetching the model" must be visible even offline
	backoff := downloadRetryBackoff
	waiting := false
	mismatches := 0
	for {
		fetched, err := d.attempt(ctx, client)
		if errors.Is(err, errDownloadElsewhere) {
			if !waiting {
				waiting = true
				d.mu.Lock()
				d.err = err
				d.got, d.total = 0, 0 // this process transfers nothing while it waits
				d.mu.Unlock()
				d.lg.Info("local embedder: another process is downloading the model; waiting for it", zap.String("dest", d.dest))
				d.rep.progress(0, 0, "waiting for another process's download")
			}
			select {
			case <-ctx.Done():
				d.rep.finish(ctx, ctx.Err())
				return
			case <-time.After(downloadPollInterval):
			}
			continue
		}
		if waiting {
			waiting = false
			backoff = downloadRetryBackoff // a takeover starts its own retries
		}
		if err == nil {
			// Every install path ends at the installed file's size: a
			// verified .part, a 416, a transfer, another process's file.
			if st, err := os.Stat(d.dest); err == nil {
				d.mu.Lock()
				d.got, d.total = st.Size(), st.Size()
				d.mu.Unlock()
			}
			d.mu.Lock()
			d.done = true
			got, total := d.got, d.total
			d.mu.Unlock()
			if fetched {
				d.lg.Info("local embedder: model downloaded", zap.String("dest", d.dest))
			} else {
				d.lg.Info("local embedder: model installed by another process", zap.String("dest", d.dest))
			}
			d.rep.progress(got, total, "")
			d.rep.finish(ctx, nil)
			return
		}
		if ctx.Err() != nil {
			d.rep.finish(ctx, err) // terminal cancelled; the .part resumes next boot
			return
		}
		// The first mismatch may come from a stale .part, now removed; a
		// second one is the source's own file.
		if errors.Is(err, errChecksumMismatch) {
			if mismatches++; mismatches >= 2 {
				d.mu.Lock()
				d.err = fmt.Errorf("%w (%v)", errDownloadStopped, err)
				d.mu.Unlock()
				d.lg.Error("local embedder: model download stopped, the source fails the checksum",
					zap.String("url", d.url), zap.String("dest", d.dest), zap.Error(err))
				d.rep.finish(ctx, errDownloadStopped)
				return
			}
		}
		d.mu.Lock()
		d.err = err
		d.mu.Unlock()
		d.lg.Warn("local embedder: model download failed, will retry",
			zap.Duration("backoff", backoff), zap.Error(err))
		// Not terminal — the download retries; the row stays
		// running with a generic status (real error carries paths/
		// response bodies and stays in the log above).
		d.rep.progress(-1, -1, "download failed; retrying — see server log")
		select {
		case <-ctx.Done():
			d.rep.finish(ctx, ctx.Err()) // stops the heartbeat
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
	}
}

// attempt installs the model unless another process has, under the
// cross-process download lock. fetched is false when the file was
// already in place.
func (d *modelDownload) attempt(ctx context.Context, client *http.Client) (fetched bool, err error) {
	if err := os.MkdirAll(filepath.Dir(d.dest), 0o755); err != nil {
		return false, err
	}
	if _, err := os.Stat(d.dest); err == nil {
		return false, nil
	}
	// The lock file is never removed: unlinking it under a lock lets the
	// next process lock a fresh inode while the old one is still held.
	ok, err := d.lock.TryLock()
	if err != nil {
		return false, fmt.Errorf("lock %s: %w", d.lock.Path(), err)
	}
	if !ok {
		return false, errDownloadElsewhere
	}
	defer func() {
		if err := d.lock.Unlock(); err != nil {
			d.lg.Warn("local embedder: model download lock not released; this process keeps it until the next attempt or exit",
				zap.String("lock", d.lock.Path()), zap.Error(err))
		}
	}()
	d.mu.Lock()
	tookOver := errors.Is(d.err, errDownloadElsewhere)
	if tookOver {
		d.err = nil
	}
	d.mu.Unlock()
	if tookOver {
		d.rep.progress(-1, -1, "")
	}
	// The holder may have installed the file between the stat and the lock.
	if _, err := os.Stat(d.dest); err == nil {
		return false, nil
	}
	return true, d.fetch(ctx, client)
}

// fetch streams the model into the .part and installs it. Caller holds
// the download lock.
func (d *modelDownload) fetch(ctx context.Context, client *http.Client) error {
	part := d.dest + ".part"

	// Resume: re-hash whatever is already on disk so the streaming
	// checksum stays valid, then ask for the remainder.
	h := sha256.New()
	var offset int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 {
		if err := hashFilePrefix(ctx, part, st.Size(), h); err == nil {
			offset = st.Size()
		} else if ctx.Err() != nil {
			return ctx.Err() // cancelled mid-hash: the .part is intact
		} else {
			_ = os.Remove(part)
			h = sha256.New()
		}
	}
	// A previous run may have been interrupted between the last byte
	// and the rename — if the .part already verifies, install it with
	// no network round-trip (a Range request at the full size would
	// draw a 416 and wedge the retry loop forever).
	if offset > 0 && d.sha != "" && hex.EncodeToString(h.Sum(nil)) == d.sha {
		return os.Rename(part, d.dest)
	}

	// The stall watchdog cuts a request that sends nothing, headers
	// included, so the attempt fails and frees the lock.
	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stall := time.AfterFunc(downloadStallTimeout, func() { cancel(errDownloadStalled) })
	defer stall.Stop()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, d.url, nil)
	if err != nil {
		return err
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := client.Do(req)
	if err != nil {
		return stallCause(reqCtx, err)
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
	case offset > 0 && resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		// The server says our offset is past the end — the .part is
		// (at least) complete. With no pinned sha (the verified case
		// installed above, pre-flight) trust the server; a pinned sha
		// that failed the pre-flight check means corruption — restart.
		if d.sha == "" {
			d.lg.Warn("local embedder: server reports download complete; installing unverified .part",
				zap.String("dest", d.dest))
			return os.Rename(part, d.dest)
		}
		_ = os.Remove(part)
		return fmt.Errorf("GET %s: %s (corrupt complete .part removed, restarting)", d.url, resp.Status)
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
	d.rep.progress(offset, total, "")

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
	if err := d.stream(reqCtx, f, resp.Body, h, offset, stall); err != nil {
		f.Close()
		return stallCause(reqCtx, err)
	}
	if err := f.Close(); err != nil {
		return err
	}

	if d.sha != "" {
		if got := hex.EncodeToString(h.Sum(nil)); got != d.sha {
			_ = os.Remove(part)
			return fmt.Errorf("%w: got %s want %s", errChecksumMismatch, got, d.sha)
		}
	}
	return os.Rename(part, d.dest)
}

// stallCause names a request the stall watchdog cut.
func stallCause(ctx context.Context, err error) error {
	if errors.Is(context.Cause(ctx), errDownloadStalled) {
		return fmt.Errorf("%w: no data for %s", errDownloadStalled, downloadStallTimeout)
	}
	return err
}

// stream copies body → file + hash, updating progress and logging it
// every ~5s. Every chunk re-arms the stall watchdog.
func (d *modelDownload) stream(ctx context.Context, f *os.File, body io.Reader, h hash.Hash, got int64, stall *time.Timer) error {
	w := io.MultiWriter(f, h)
	buf := make([]byte, 1<<20)
	lastLog := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := body.Read(buf)
		if n > 0 {
			stall.Reset(downloadStallTimeout)
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
				d.rep.progress(got, total, "")
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

func hashFilePrefix(ctx context.Context, path string, n int64, h hash.Hash) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// Chunked so Close is not held for a whole multi-hundred-MB prefix.
	const chunk = 8 << 20
	for n > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		step := min(n, chunk)
		if _, err := io.CopyN(h, f, step); err != nil {
			return err
		}
		n -= step
	}
	return nil
}
