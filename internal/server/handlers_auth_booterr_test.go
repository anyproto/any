package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"

	anystore "github.com/anyproto/any-store/v2"
	"github.com/anyproto/any-sync/app/logger"
	"github.com/anyproto/go-sqlite"
	"github.com/labstack/echo/v4"

	"github.com/anyproto/any/internal/api"
	"github.com/anyproto/any/internal/config"
)

// The boot failures a host repairs differently each get a code: a
// failed sdk.db integrity check, and a full disk from the account dir,
// the device key, an any-store v2 file or an any-sync sqlite store. No
// body names a path, and a check cut short by a cancelled boot is not a
// damaged store.
func TestAuthBootError_TypedRepairs(t *testing.T) {
	e := echo.New()
	const dir = "/data/accounts/A1"
	enospc := func(op, path string) error {
		return &os.PathError{Op: op, Path: path, Err: syscall.ENOSPC}
	}
	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
		posix  bool // syscall.ENOSPC never comes back on Windows
	}{
		"sdk quick check": {
			err:    fmt.Errorf("open sdk: anysyncsdk: open sdk db: %w", fmt.Errorf("%w: page 7: checksum mismatch", anystore.ErrQuickCheckFailed)),
			status: http.StatusInternalServerError, code: "sdk.store_corrupt",
		},
		"sdk page checksum": {
			err:    fmt.Errorf("open sdk: anysyncsdk: open files store: filestore: open files_local: %w", fmt.Errorf("btree: page 1759: %w", anystore.ErrPageIntegrity)),
			status: http.StatusInternalServerError, code: "sdk.store_corrupt",
		},
		"sdk quick check cancelled": {
			err:    fmt.Errorf("open sdk: anysyncsdk: open sdk db: %w", fmt.Errorf("%w: %w", anystore.ErrQuickCheckFailed, context.Canceled)),
			status: http.StatusInternalServerError, code: "internal",
		},
		"account dir full": {
			err:    fmt.Errorf("create account dir %s: %w", dir, enospc("mkdir", dir)),
			status: http.StatusInsufficientStorage, code: "storage.no_space", posix: true,
		},
		"device key full": {
			err:    fmt.Errorf("write %s: %w", dir+"/device.key", enospc("write", dir+"/device.key")),
			status: http.StatusInsufficientStorage, code: "storage.no_space", posix: true,
		},
		"sdk db full": {
			err:    fmt.Errorf("open sdk: anysyncsdk: open sdk db: %w", enospc("write", dir+"/sdk/sdk.db-wal")),
			status: http.StatusInsufficientStorage, code: "storage.no_space", posix: true,
		},
		"sqlite full": {
			err:    fmt.Errorf("open sdk: anysyncx: app start: %w", sqlite.ResultFull.ToError()),
			status: http.StatusInsufficientStorage, code: "storage.no_space",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.posix && runtime.GOOS == "windows" {
				t.Skip("ENOSPC is a POSIX errno")
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(httptest.NewRequest(http.MethodPost, "/", nil), rec)
			if err := (&deps{}).authBootError(c, tc.err); err != nil {
				t.Fatalf("handler returned %v", err)
			}
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if got := errEnvCode(t, rec.Body.Bytes()); got != tc.code {
				t.Fatalf("code = %q, want %q: %s", got, tc.code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), dir) {
				t.Fatalf("body names a path: %s", rec.Body.String())
			}
		})
	}
}

// A managed login over a damaged sdk.db answers sdk.store_corrupt with
// no path in the body, and the server stays up unauthorized. Needs the
// staging nodeconf like every other SDK-booting test here.
func TestAuth_ManagedBootStoreCorrupt(t *testing.T) {
	if _, err := config.LoadNodeconf(config.Network{NodeconfPath: stagingPath}); err != nil {
		t.Skipf("staging config not available: %v", err)
	}
	d := newUnauthorizedDeps(t)
	d.cfg.Mode = config.ModeManaged
	d.controlToken = "tok"
	e := buildEcho(d)
	tok := map[string]string{api.ControlTokenHeader: "tok"}

	rec := doJSONH(t, e, http.MethodPost, "/v1/auth", `{}`, tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("managed generate: %d %s", rec.Code, rec.Body.String())
	}
	var first api.AuthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	d.teardownEngine(logger.NewNamed("test"), api.SubscribeClosedDeauthorized)

	dir := config.AccountDir(d.root, first.AccountId)
	tearStorePage2(t, filepath.Join(dir, "sdk", "sdk.db"))

	rec = doJSONH(t, e, http.MethodPost, "/v1/auth", `{"mnemonic":`+strconv.Quote(first.Mnemonic)+`}`, tok)
	expectAuthError(t, rec, http.StatusInternalServerError, "sdk.store_corrupt", "")
	if strings.Contains(rec.Body.String(), d.root) {
		t.Fatalf("body names a path: %s", rec.Body.String())
	}
	rec = doJSON(t, e, http.MethodGet, "/v1/auth", "")
	var st api.AuthStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Authorized {
		t.Fatalf("server authorized after a refused boot: %+v", st)
	}
}
