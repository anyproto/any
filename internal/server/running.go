package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"

	"github.com/anyproto/any/internal/config"
)

// Running describes a server process holding an account's instance
// lock under a root. Found by probing the lock itself, so it is proof
// of life: a crashed holder's lock is released by the kernel and the
// dir reads as not running, whatever server.pid says.
type Running struct {
	// Account is the dir name (the account id); "" for the legacy
	// flat-root account.
	Account string
	Dir     string
	// PID is the holder's pid from server.pid; 0 when unknown.
	PID int
	// Addr is the holder's bound address from server.addr; "" when
	// unknown (a server older than the file, or one that never bound).
	Addr string
}

// DefaultAccountSelector names the legacy flat-root account to
// FindRunning's filter, which otherwise matches account dirs by id.
const DefaultAccountSelector = "default"

// FindRunning lists the servers currently serving accounts under root:
// the root itself (legacy layout) and every account dir, filtered to
// account when non-empty (DefaultAccountSelector picks the root).
// Neither a wallet nor a device key is consulted — only a held
// server.lock counts — so it finds standalone and managed servers
// alike, and never an unauthorized one (an engine that has not booted
// holds no lock). The probe takes and releases each lock for
// microseconds; Acquire retries briefly so a booting server never
// mistakes a probe for a holder.
func FindRunning(root, account string) ([]Running, error) {
	var dirs []string
	if _, err := os.Stat(config.LockPath(root)); err == nil {
		dirs = append(dirs, root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if _, err := os.Stat(config.LockPath(dir)); err == nil {
			dirs = append(dirs, dir)
		}
	}

	var out []Running
	for _, dir := range dirs {
		acct := ""
		if dir != root {
			acct = filepath.Base(dir)
		}
		switch account {
		case "":
		case DefaultAccountSelector:
			if acct != "" {
				continue
			}
		default:
			if acct != account {
				continue
			}
		}
		held, err := lockHeld(dir)
		if err != nil {
			return nil, err
		}
		if !held {
			continue
		}
		out = append(out, Running{
			Account: acct,
			Dir:     dir,
			PID:     readHolderPID(config.PIDPath(dir)),
			Addr:    readAddrFile(config.AddrPath(dir)),
		})
	}
	return out, nil
}

// lockHeld probes an account dir's instance lock: a lock this process
// can take is free (released at once), one it cannot is held by a live
// server.
func lockHeld(dir string) (bool, error) {
	fl := flock.New(config.LockPath(dir))
	ok, err := fl.TryLock()
	if err != nil {
		return false, fmt.Errorf("probe %s: %w", config.LockPath(dir), err)
	}
	if ok {
		_ = fl.Unlock()
		return false, nil
	}
	return true, nil
}

// StopRunning asks the holder to exit (SIGTERM — the same graceful
// path Ctrl-C takes) and waits for its lock to be released. Windows has
// no signal to send; stopping there is the user's job.
func StopRunning(r Running, wait time.Duration) error {
	if r.PID <= 0 {
		return fmt.Errorf("server holding %s has no readable pid", config.LockPath(r.Dir))
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("stopping by signal is not available on Windows — stop the server (pid %d) with Ctrl-C in its terminal or from Task Manager", r.PID)
	}
	proc, err := os.FindProcess(r.PID)
	if err != nil {
		return fmt.Errorf("find pid %d: %w", r.PID, err)
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signal pid %d: %w", r.PID, err)
	}
	deadline := time.Now().Add(wait)
	for {
		held, err := lockHeld(r.Dir)
		if err != nil {
			return err
		}
		if !held {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server pid %d did not exit within %s", r.PID, wait)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// writeAddrFile records the bound address beside the pid file.
// Best-effort — the lock is the contract, this is a convenience for
// the CLI.
func writeAddrFile(dir, addr string) {
	if err := os.WriteFile(config.AddrPath(dir), []byte(addr+"\n"), 0o600); err != nil {
		pidLockLog.Warn("write addr file: " + err.Error())
	}
}

// readAddrFile returns the recorded address, "" for every failure.
func readAddrFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
