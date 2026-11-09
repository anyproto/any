package embedded

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/go-sqlite"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
	"github.com/anyproto/any/internal/indexer"
)

// Each case copies the wraps internal/server and internal/indexer apply;
// TestStartCodeStoreCorrupt covers one real chain end to end.
func TestCode(t *testing.T) {
	enospc := func(op string) error {
		return &os.PathError{Op: op, Path: "/data/A1/x", Err: syscall.ENOSPC}
	}
	sdkOpen := func(err error) error {
		return &BootError{Err: fmt.Errorf("open sdk: anysyncsdk: open sdk db: %w", err)}
	}
	indexRefusal := indexer.RefuseDamaged(fmt.Errorf("btree: page 3: %w", anystore.ErrPageIntegrity))
	rebuildFailed := func(err error) error {
		return &BootError{Err: fmt.Errorf("open indexer: %w",
			fmt.Errorf("rebuild search index: %w (%w)", err, indexRefusal))}
	}
	for name, tc := range map[string]struct {
		err   error
		want  int32
		posix bool // syscall.ENOSPC never comes back on Windows
	}{
		"nil":                             {nil, CodeOK, false},
		"already running":                 {ErrAlreadyRunning, CodeAlreadyRunning, false},
		"bad data dir":                    {ErrBadDataDir, CodeBadDataDir, false},
		"bad data dir, wrapped":           {fmt.Errorf("ensure: %w", ErrBadDataDir), CodeBadDataDir, false},
		"bad data dir, not creatable":     {fmt.Errorf("%w: %w", ErrBadDataDir, &os.PathError{Op: "mkdir", Path: "/x", Err: syscall.ENOTDIR}), CodeBadDataDir, false},
		"bad options":                     {fmt.Errorf("%w: managed mode needs a control token", ErrBadOptions), CodeBadOptions, false},
		"boot error":                      {&BootError{Err: errors.New("listen: address in use")}, CodeBootFailed, false},
		"unknown error":                   {errors.New("some other error"), CodeBootFailed, false},
		"index rebuild required, bare":    {indexer.ErrIndexRebuildRequired, CodeIndexRebuildRequired, false},
		"index rebuild failed":            {rebuildFailed(errors.New("remove index db: permission denied")), CodeIndexRebuildRequired, false},
		"index rebuild failed, disk full": {rebuildFailed(enospc("write")), CodeStorageFull, true},
		"sdk.db quick check":              {sdkOpen(fmt.Errorf("%w: %w", anystore.ErrQuickCheckFailed, fmt.Errorf("btree: page 2: %w", anystore.ErrPageIntegrity))), CodeStoreCorrupt, false},
		"sdk.db quick check read error":   {sdkOpen(fmt.Errorf("%w: %w", anystore.ErrQuickCheckFailed, syscall.EIO)), CodeBootFailed, false},
		"sdk.db page checksum":            {&BootError{Err: fmt.Errorf("open sdk: anysyncsdk: open files store: filestore: open files_local: %w", fmt.Errorf("btree: page 1759: %w", anystore.ErrPageIntegrity))}, CodeStoreCorrupt, false},
		"sdk.db quick check cancelled":    {sdkOpen(fmt.Errorf("%w: %w", anystore.ErrQuickCheckFailed, context.Canceled)), CodeBootFailed, false},
		"sdk.db quick check timed out":    {sdkOpen(fmt.Errorf("%w: %w", anystore.ErrQuickCheckFailed, context.DeadlineExceeded)), CodeBootFailed, false},
		"sdk.db disk full":                {sdkOpen(enospc("write")), CodeStorageFull, true},
		"any-sync store full":             {&BootError{Err: fmt.Errorf("open sdk: anysyncx: app start: %w", sqlite.ResultFull.ToError())}, CodeStorageFull, false},
		"account dir full":                {&BootError{Err: fmt.Errorf("create account dir: %w", enospc("mkdir"))}, CodeStorageFull, true},
		"data dir full":                   {fmt.Errorf("%w: %w", ErrBadDataDir, enospc("mkdir")), CodeStorageFull, true},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.posix && runtime.GOOS == "windows" {
				t.Skip("ENOSPC is a POSIX errno")
			}
			if got := Code(tc.err); got != tc.want {
				t.Fatalf("Code(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// A standalone relaunch over a torn sdk.db reports CodeStoreCorrupt, with
// the dirty sentinel (the quick check fails) and without it (the first read
// fails its page checksum): the real boot chain, not a built one. Needs the
// gitignored staging nodeconf to create the account, like the server's
// SDK-booting tests.
func TestStartCodeStoreCorrupt(t *testing.T) {
	nodeconf, err := os.ReadFile("../../staging.yml")
	if err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	for name, sentinel := range map[string]bool{"quick check": true, "page checksum": false} {
		t.Run(name, func(t *testing.T) {
			resetState(t)
			defer resetState(t)

			root := t.TempDir()
			addr, err := start(root, string(nodeconf))
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			client := &http.Client{Timeout: time.Minute}
			resp, err := client.Post("http://"+addr+"/v1/auth", "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			var auth api.AuthResponse
			err = json.NewDecoder(resp.Body).Decode(&auth)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK || auth.AccountId == "" {
				t.Fatalf("create account: %d %+v %v", resp.StatusCode, auth, err)
			}
			Stop(true)

			// Tear page 2, the system namespace root: the quick check reads
			// it on a dirty open, the first catalog read otherwise.
			db := filepath.Join(config.AccountDir(root, auth.AccountId), "sdk", "sdk.db")
			b, err := os.ReadFile(db)
			if err != nil {
				t.Fatal(err)
			}
			const pageSize = 4096
			if len(b) < 2*pageSize {
				t.Fatalf("sdk.db is %d bytes, want at least two pages", len(b))
			}
			for i := pageSize + 100; i < pageSize+600; i++ {
				b[i] ^= 0xff
			}
			if err := os.WriteFile(db, b, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(db + ".lock"); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if sentinel {
				if err := os.WriteFile(db+".lock", nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err = start(root, string(nodeconf))
			if got := Code(err); got != CodeStoreCorrupt {
				t.Fatalf("relaunch over a torn sdk.db: Code(%v) = %d, want %d", err, got, CodeStoreCorrupt)
			}
		})
	}
}
