//go:build llamacpp && !android && !ios

package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func waitDownload(t *testing.T, d *modelDownload) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.Status() == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("download did not finish: %v", d.Status())
}

func TestModelDownload_HappyPath(t *testing.T) {
	body := []byte(strings.Repeat("gguf-bytes ", 1000))
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client(), nil)
	defer d.Close()
	waitDownload(t, d)

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("downloaded content differs")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part should be renamed away")
	}
}

func TestModelDownload_ShaMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the expected bytes"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "m.gguf")
	d := startModelDownload(srv.URL, dest, strings.Repeat("ab", 32), srv.Client(), nil)
	defer d.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := d.Status(); err != nil && strings.Contains(err.Error(), "sha256 mismatch") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := d.Status(); err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("want sha256 mismatch error, got %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Error("dest must not exist on mismatch")
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Error(".part must be removed on mismatch")
	}
}

func TestModelDownload_Resume(t *testing.T) {
	body := []byte(strings.Repeat("0123456789", 500))
	sum := sha256.Sum256(body)
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		if gotRange == "" {
			w.Write(body)
			return
		}
		off, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(gotRange, "bytes="), "-"), 10, 64)
		if err != nil {
			t.Errorf("bad range %q", gotRange)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes "+strconv.FormatInt(off, 10)+"-")
		w.WriteHeader(http.StatusPartialContent)
		w.Write(body[off:])
	}))
	defer srv.Close()

	// Pre-seed half the body as an interrupted .part.
	dest := filepath.Join(t.TempDir(), "m.gguf")
	if err := os.WriteFile(dest+".part", body[:2500], 0o644); err != nil {
		t.Fatal(err)
	}

	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client(), nil)
	defer d.Close()
	waitDownload(t, d)

	if gotRange != "bytes=2500-" {
		t.Errorf("want Range: bytes=2500-, got %q", gotRange)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(body) {
		t.Error("resumed content differs")
	}
}

func TestModelDownload_StatusProgress(t *testing.T) {
	d := &modelDownload{got: 256 << 20, total: 640 << 20}
	err := d.Status()
	if err == nil {
		t.Fatal("in-flight download must report a status error")
	}
	if !strings.Contains(err.Error(), "40%") || !strings.Contains(err.Error(), "256/640 MB") {
		t.Errorf("unexpected progress string: %v", err)
	}
	d.done = true
	if d.Status() != nil {
		t.Error("done download must report nil")
	}
}

// The download reports its lifecycle for the process view: started
// once (with the byte total from Content-Length), progress while
// streaming, done at the end — Done/Total in bytes, Name the model
// file name.
func TestModelDownload_ReportsProcess(t *testing.T) {
	body := []byte(strings.Repeat("gguf-bytes ", 1000))
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer srv.Close()

	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Explicit length (like a real model host) — a body past the
		// server buffer otherwise goes chunked and Total stays unknown.
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Write(body)
	})

	var frames frameLog
	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client(), frames.report)
	defer d.Close()
	waitDownload(t, d)
	<-d.finished

	updates := frames.all()
	if len(updates) < 2 {
		t.Fatalf("reported %+v, want at least started+done", updates)
	}
	first, last := updates[0], updates[len(updates)-1]
	// started fires at download start — before the response headers, so
	// its Total is still unknown; the byte total arrives on progress.
	if first.Phase != ProcessStarted || first.Kind != ProcessKindModelDownload || first.Name != "m.gguf" {
		t.Errorf("first = %+v, want started model_download m.gguf", first)
	}
	sawTotal := false
	for _, u := range updates {
		if u.Phase == ProcessProgress && u.Total == int64(len(body)) {
			sawTotal = true
		}
	}
	if !sawTotal {
		t.Errorf("no progress frame carried Total=%d (Content-Length): %+v", len(body), updates)
	}
	if last.Phase != ProcessDone || last.Done != int64(len(body)) || last.Total != int64(len(body)) {
		t.Errorf("last = %+v, want done with Done=Total=%d", last, len(body))
	}
}

// A .part interrupted between the last byte and the rename installs
// on the next boot with no network round-trip — a Range request at
// the full size would draw a 416 and wedge the retry loop forever.
func TestModelDownload_CompletePartInstallsOffline(t *testing.T) {
	body := []byte(strings.Repeat("gguf-bytes ", 1000))
	sum := sha256.Sum256(body)
	// Every network attempt fails — the install must not need one.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part", body, 0o644); err != nil {
		t.Fatal(err)
	}
	var frames frameLog
	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client(), frames.report)
	defer d.Close()
	waitDownload(t, d)

	got, err := os.ReadFile(dest)
	if err != nil || string(got) != string(body) {
		t.Fatalf("complete .part not installed: %v", err)
	}
	<-d.finished
	if f, n := frames.last(), int64(len(body)); f.Phase != ProcessDone || f.Done != n || f.Total != n {
		t.Fatalf("last frame = %+v, want done at %d bytes", f, n)
	}
}

// frameLog collects a download's process frames.
type frameLog struct {
	mu      sync.Mutex
	updates []ProcessUpdate
}

func (l *frameLog) report(u ProcessUpdate) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.updates = append(l.updates, u)
}

func (l *frameLog) all() []ProcessUpdate {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]ProcessUpdate(nil), l.updates...)
}

// last is the newest frame (zero before the first).
func (l *frameLog) last() ProcessUpdate {
	all := l.all()
	if len(all) == 0 {
		return ProcessUpdate{}
	}
	return all[len(all)-1]
}

// withMessage is the frames that carry msg.
func (l *frameLog) withMessage(msg string) []ProcessUpdate {
	var out []ProcessUpdate
	for _, u := range l.all() {
		if u.Message == msg {
			out = append(out, u)
		}
	}
	return out
}

const waitingMessage = "waiting for another process's download"

// With no sha pinned, a server that answers 416 to the resume range
// installs the .part as is, sized like any other install.
func TestModelDownload_RangePastEndInstallsUnverified(t *testing.T) {
	body := []byte(strings.Repeat("gguf-bytes ", 1000))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			t.Errorf("want a resume range")
		}
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest+".part", body, 0o644); err != nil {
		t.Fatal(err)
	}
	var frames frameLog
	d := startModelDownload(srv.URL, dest, "", srv.Client(), frames.report)
	defer d.Close()
	waitDownload(t, d)
	<-d.finished
	if f, n := frames.last(), int64(len(body)); f.Phase != ProcessDone || f.Done != n || f.Total != n {
		t.Fatalf("last frame = %+v, want done at %d bytes", f, n)
	}
}

// Close during a retry backoff joins the goroutine and ends the
// process row: the reporter's heartbeat must not outlive the download.
func TestModelDownload_CloseDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()

	var frames frameLog
	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, "", srv.Client(), frames.report)

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := d.Status(); err != nil && strings.Contains(err.Error(), "retrying") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("download never reached the retry backoff: %v", d.Status())
		}
		time.Sleep(10 * time.Millisecond)
	}

	d.Close()
	select {
	case <-d.finished:
	default:
		t.Fatal("Close returned before the download goroutine")
	}
	if last := frames.last(); last.Phase != ProcessCancelled {
		t.Fatalf("last frame = %+v, want cancelled", last)
	}
	d.rep.mu.Lock()
	finished := d.rep.finished
	d.rep.mu.Unlock()
	if !finished {
		t.Fatal("process reporter still running after Close")
	}
}

// holdDownloadLock takes the download lock the way another process
// would: flock(2) locks belong to an open file description, so a second
// handle conflicts even inside this process.
func holdDownloadLock(t *testing.T, dest string) *flock.Flock {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	fl := flock.New(dest + ".lock")
	ok, err := fl.TryLock()
	if err != nil || !ok {
		t.Fatalf("take the download lock: ok=%v err=%v", ok, err)
	}
	t.Cleanup(func() { _ = fl.Unlock() })
	return fl
}

func shortDownloadPoll(t *testing.T) {
	t.Helper()
	prev := downloadPollInterval
	downloadPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { downloadPollInterval = prev })
}

// While another process holds the lock, this one never touches the
// network or the .part, reports the wait once, and is done once the
// holder installs the file.
func TestModelDownload_WaitsForLockHolder(t *testing.T) {
	shortDownloadPoll(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("must not be fetched"))
	}))
	defer srv.Close()

	var frames frameLog
	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	holder := holdDownloadLock(t, dest)
	d := startModelDownload(srv.URL, dest, "", srv.Client(), frames.report)
	defer d.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := d.Status(); err != nil && errors.Is(err, errDownloadElsewhere) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("download never reported the other holder: %v", d.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(20 * downloadPollInterval) // many polls
	if waits := len(frames.withMessage(waitingMessage)); waits != 1 {
		t.Fatalf("%d waiting frames, want one for the change of state", waits)
	}
	// Installed the way the holder does it: write aside, then rename.
	if err := os.WriteFile(dest+".holder", []byte("installed by the holder"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dest+".holder", dest); err != nil {
		t.Fatal(err)
	}
	if err := holder.Unlock(); err != nil {
		t.Fatal(err)
	}
	waitDownload(t, d)

	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests while another process held the lock", n)
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part touched while another process held the lock: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "installed by the holder" {
		t.Fatalf("dest = %q, want the holder's file", got)
	}
	<-d.finished
	if last, n := frames.last(), int64(len("installed by the holder")); last.Phase != ProcessDone || last.Done != n || last.Total != n {
		t.Fatalf("last frame = %+v, want done at the installed file's %d bytes", last, n)
	}
}

// A holder that goes away without installing (a crashed download frees
// its lock) leaves the download to the next process.
func TestModelDownload_TakesOverReleasedLock(t *testing.T) {
	shortDownloadPoll(t)
	body := []byte(strings.Repeat("gguf-bytes ", 1000))
	sum := sha256.Sum256(body)
	requested, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(requested) })
		select {
		case <-release:
			w.Write(body)
		case <-r.Context().Done(): // a failed assertion must not hang srv.Close
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	holder := holdDownloadLock(t, dest)
	d := startModelDownload(srv.URL, dest, hex.EncodeToString(sum[:]), srv.Client(), nil)
	defer d.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(d.Status(), errDownloadElsewhere) {
		if time.Now().After(deadline) {
			t.Fatalf("status %v while another process held the lock", d.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := holder.Unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		t.Fatal("no takeover after the holder left")
	}
	if err := d.Status(); errors.Is(err, errDownloadElsewhere) {
		t.Fatalf("status %v while this process downloads", err)
	}
	close(release)
	waitDownload(t, d)

	got, err := os.ReadFile(dest)
	if err != nil || string(got) != string(body) {
		t.Fatalf("download after the holder left: %v", err)
	}
	// The lock stays free for the next process.
	again := flock.New(dest + ".lock")
	if ok, err := again.TryLock(); err != nil || !ok {
		t.Fatalf("lock still held after the download: ok=%v err=%v", ok, err)
	}
	_ = again.Unlock()
}

// A transfer that stops sending is cut by the stall watchdog: the
// attempt fails into the retry backoff and frees the lock for the other
// processes under the root.
func TestModelDownload_StallCutsTransfer(t *testing.T) {
	prev := downloadStallTimeout
	downloadStallTimeout = 100 * time.Millisecond
	t.Cleanup(func() { downloadStallTimeout = prev })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.Write([]byte(strings.Repeat("x", 1000)))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // live connection, no more bytes
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, "", srv.Client(), nil)
	defer d.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(d.Status(), errDownloadStalled) {
		if time.Now().After(deadline) {
			t.Fatalf("stalled transfer never cut: %v", d.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	fl := flock.New(dest + ".lock")
	if ok, err := fl.TryLock(); err != nil || !ok {
		t.Fatalf("lock still held after the stall: ok=%v err=%v", ok, err)
	}
	_ = fl.Unlock()
}

// Entering the wait drops the counters of this process's earlier
// attempt: it transfers nothing while another process downloads.
func TestModelDownload_WaitResetsCounters(t *testing.T) {
	shortDownloadPoll(t)
	prevStall, prevBackoff := downloadStallTimeout, downloadRetryBackoff
	downloadStallTimeout, downloadRetryBackoff = 100*time.Millisecond, time.Second
	t.Cleanup(func() { downloadStallTimeout, downloadRetryBackoff = prevStall, prevBackoff })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		w.Write([]byte(strings.Repeat("x", 1000)))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()

	var frames frameLog
	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, "", srv.Client(), frames.report)
	defer d.Close()
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(d.Status(), errDownloadStalled) { // attempt 1 got 1000 of 100000 bytes
		if time.Now().After(deadline) {
			t.Fatalf("first attempt never stalled: %v", d.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	holdDownloadLock(t, dest) // another process takes over during the backoff
	for !errors.Is(d.Status(), errDownloadElsewhere) {
		if time.Now().After(deadline) {
			t.Fatalf("never waited for the other process: %v", d.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	waits := frames.withMessage(waitingMessage)
	if len(waits) == 0 {
		t.Fatal("no waiting frame")
	}
	if u := waits[0]; u.Done != 0 || u.Total != 0 {
		t.Fatalf("waiting frame %+v carries the earlier attempt's counters", u)
	}
}

// A takeover after a wait starts its own retry sequence: one failure
// right after it costs the first backoff, not the one this process had
// grown to before it waited.
func TestModelDownload_TakeoverResetsBackoff(t *testing.T) {
	shortDownloadPoll(t)
	prev := downloadRetryBackoff
	downloadRetryBackoff = 50 * time.Millisecond
	t.Cleanup(func() { downloadRetryBackoff = prev })
	var mu sync.Mutex
	var hits []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, time.Now())
		mu.Unlock()
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	requests := func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Time(nil), hits...)
	}
	waitRequests := func(n int) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for len(requests()) < n {
			if time.Now().After(deadline) {
				t.Fatalf("%d requests, want %d", len(requests()), n)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	dest := filepath.Join(t.TempDir(), "models", "m.gguf")
	d := startModelDownload(srv.URL, dest, "", srv.Client(), nil)
	defer d.Close()
	waitRequests(4) // backoff grown 50 → 100 → 200 → 400; the next wait is 400 ms
	other := flock.New(dest + ".lock")
	deadline := time.Now().Add(5 * time.Second)
	for ok, _ := other.TryLock(); !ok; ok, _ = other.TryLock() { // free once attempt 4 returns
		if time.Now().After(deadline) {
			t.Fatal("never took the lock during the backoff")
		}
		time.Sleep(time.Millisecond)
	}
	for !errors.Is(d.Status(), errDownloadElsewhere) {
		if time.Now().After(deadline) {
			t.Fatalf("never waited for the other process: %v", d.Status())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := other.Unlock(); err != nil {
		t.Fatal(err)
	}
	waitRequests(6) // the takeover's request fails, then one backoff
	got := requests()
	if gap := got[5].Sub(got[4]); gap > 300*time.Millisecond {
		t.Fatalf("retry %s after the takeover failed, want the first backoff (50 ms)", gap)
	}
}
