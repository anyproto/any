package server

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/anyproto/any-sync/app/logger"
	"github.com/gofrs/flock"
	"go.uber.org/zap"

	"github.com/anyproto/any/internal/config"
)

var pidLockLog = logger.NewNamed("pidlock")

// Lock is the single-instance lock on an account dir: an exclusive OS
// file lock on <dir>/server.lock (flock(2) on unix, LockFileEx with
// LOCKFILE_FAIL_IMMEDIATELY on Windows), plus <dir>/server.pid naming
// the holder for error messages.
//
// The kernel releases the lock when the process exits by any means, so
// there is no stale-lock reclaim and no liveness probe: a crashed holder
// blocks nobody. server.pid is advisory — it never decides who holds the
// lock, and it can name a dead process whenever the lock is free.
//
// Neither file is removed, on release or on crash. Unlinking a file
// under an OS lock lets the next process create a fresh inode and lock
// THAT, so both would hold "the" lock.
//
// The lock file is opened only here: on aix/solaris/illumos gofrs/flock
// falls back to POSIX fcntl records, which any close of any descriptor
// to the file drops. Those aren't platforms we ship, but a second opener
// would be a trap on the ones that are, too.
type Lock struct {
	fl *flock.Flock
}

// acquireProbeRetries × acquireProbeDelay bounds how long Acquire keeps
// retrying a refused lock before reporting ErrLocked. A CLI probe
// (FindRunning) holds the lock for microseconds; a real holder keeps
// it for the process lifetime, so a few short retries tell the two
// apart without ever waiting on a genuinely held lock for long.
const (
	acquireProbeRetries = 5
	acquireProbeDelay   = 20 * time.Millisecond
)

// Acquire takes the single-instance lock for an account dir. Returns
// ErrLocked when another process holds it.
func Acquire(dir string) (*Lock, error) {
	lockPath, pidPath := config.LockPath(dir), config.PIDPath(dir)

	fl := flock.New(lockPath)
	var ok bool
	for attempt := 0; ; attempt++ {
		var err error
		ok, err = fl.TryLock()
		if err != nil {
			return nil, fmt.Errorf("lock %s: %w", lockPath, err)
		}
		if ok || attempt == acquireProbeRetries {
			break
		}
		time.Sleep(acquireProbeDelay)
	}
	if !ok {
		return nil, &ErrLocked{PID: readHolderPID(pidPath), Path: lockPath}
	}

	// Best-effort: the lock is the contract, this is the name on it. On
	// failure drop the file rather than leave a previous holder's pid to
	// be reported as ours — an absent pid reads as unknown, a wrong one
	// names an unrelated process.
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		pidLockLog.Warn("write pid file", zap.Error(err))
		_ = os.Remove(pidPath)
	}
	return &Lock{fl: fl}, nil
}

// Release drops the OS lock and closes the lock file. Safe to call from
// a defer — no-op on a nil Lock (the mobile build's bypass). The lock
// and pid files stay on disk by design.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	return l.fl.Unlock()
}

// ErrLocked is returned when Acquire finds another process holding the
// lock. PID is 0 when the holder's pid file is missing or unreadable —
// the holder writes it just after acquiring, so a racing Acquire can
// arrive first.
type ErrLocked struct {
	PID  int
	Path string
}

func (e *ErrLocked) Error() string {
	if e.PID <= 0 {
		return fmt.Sprintf("%s is locked by another process", e.Path)
	}
	return fmt.Sprintf("%s is locked by PID %d", e.Path, e.PID)
}

// readHolderPID reads the holder's pid, reporting 0 for every failure —
// an absent, empty or malformed file only costs the pid in the message.
func readHolderPID(path string) int {
	return max(readIntFile(path), 0)
}
