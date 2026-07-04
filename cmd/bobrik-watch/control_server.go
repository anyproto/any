package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
)

// bootstrapMu serializes bootstrap operations. Signals used to be delivered to
// a single goroutine, which serialized refreshes implicitly; the control server
// handles each request in its own goroutine, so two concurrent /bootstrap posts
// (or a /bootstrap racing a /bootstrap-clean) would otherwise run
// bootstrapSystemFiles against the same space at once.
var bootstrapMu sync.Mutex

// controlResponse is the JSON reply shape for every control endpoint — mirrors
// run_server.go's runResponse so the two servers stay wire-compatible when they
// merge onto one mux.
type controlResponse struct {
	OK       bool   `json:"ok"`
	FolderID string `json:"folderId,omitempty"`
	Error    string `json:"error,omitempty"`
}

// registerControlRoutes adds bobrik's control API to mux — the HTTP
// replacement for the old SIGHUP/SIGUSR1 signals, so `--bootstrap` works on
// any platform (no Unix-only signals, no PID file). Endpoints:
//
//	POST /bootstrap        — incremental (hash-gated) refresh of "System Bobrik
//	                         Files" (same path as a plain restart / boot).
//	POST /bootstrap-clean  — wipe the system folder + children, then rebuild
//	                         from scratch (corrupt/divergent-space recovery).
//
// Both run the exact bootstrapSystemFiles the boot path runs, so a running
// watcher refreshes its in-space JS without restarting. Registered onto the
// same mux as /run (see startRunRoutes in run_server.go) so both share one
// listener.
func registerControlRoutes(mux *http.ServeMux, spaceID, programTypeID, skillTypeID string) {
	mux.HandleFunc("/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeControlErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		bootstrapMu.Lock()
		defer bootstrapMu.Unlock()
		fmt.Fprintf(os.Stderr, "POST /bootstrap — refreshing System Bobrik Files (incremental)\n")
		folderID, err := bootstrapSystemFiles(spaceID, programTypeID, skillTypeID)
		if err != nil {
			writeControlErr(w, http.StatusBadGateway, "rebootstrap: "+err.Error())
			return
		}
		fmt.Fprintf(os.Stderr, "refresh complete\n")
		writeControlOK(w, folderID)
	})

	mux.HandleFunc("/bootstrap-clean", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeControlErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		bootstrapMu.Lock()
		defer bootstrapMu.Unlock()
		fmt.Fprintf(os.Stderr, "POST /bootstrap-clean — wiping and rebuilding System Bobrik Files\n")
		if err := removeSystemFiles(base, spaceID); err != nil {
			writeControlErr(w, http.StatusBadGateway, "remove system files: "+err.Error())
			return
		}
		folderID, err := bootstrapSystemFiles(spaceID, programTypeID, skillTypeID)
		if err != nil {
			writeControlErr(w, http.StatusBadGateway, "rebootstrap: "+err.Error())
			return
		}
		fmt.Fprintf(os.Stderr, "clean rebuild complete\n")
		writeControlOK(w, folderID)
	})
}

func writeControlOK(w http.ResponseWriter, folderID string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(controlResponse{OK: true, FolderID: folderID})
}

func writeControlErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(controlResponse{Error: msg})
}
