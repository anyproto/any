package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"

	agentrt "github.com/anyproto/anytype-agent-runtime/runtime"

	"github.com/anyproto/any/internal/anyrt"
)

// runRequest is the POST /run body. Either `program` (an import string like
// "meetingEnrich@v1") or `programId` (the program object's id in bobrik's
// space) selects what to run. `spaceId` is the TARGET space the program
// operates on — it is passed through as args.spaceId; module resolution and the
// program object itself live in bobrik's own space (with the usual private-
// space fallback for shared deps), so a program can act on any space without
// being deployed there.
type runRequest struct {
	Program   string         `json:"program"`
	ProgramID string         `json:"programId"`
	SpaceID   string         `json:"spaceId"`
	Args      map[string]any `json:"args"`
	Trace     bool           `json:"trace"`
	// Method (optional) calls a named export of `program` with positional
	// arguments from Call, instead of its main(). Requires Program (name).
	Method string `json:"method"`
	Call   []any  `json:"call"`
}

type runResponse struct {
	Result any    `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
	Trace  any    `json:"trace,omitempty"`
}

// registerRunRoute adds POST /run to mux. It executes a deployed program in
// bobrik's own kernel — the exact runtime path a chat-driven agent run uses
// (NewSobekRuntime + SetupAnySDKDirtyRuntime + EvalToString), so there is no
// second environment or module loader. One fresh runtime per request,
// mirroring runAgent's per-message isolation. Registered onto the same mux as
// /bootstrap and /bootstrap-clean (see registerControlRoutes in
// control_server.go) so all three share one listener.
func registerRunRoute(mux *http.ServeMux, bobrikSpaceID string) {
	mux.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeRunErr(w, http.StatusMethodNotAllowed, "POST only")
			return
		}
		var req runRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeRunErr(w, http.StatusBadRequest, "bad json: "+err.Error())
			return
		}
		if req.Program == "" && req.ProgramID == "" {
			writeRunErr(w, http.StatusBadRequest, "program (import string) or programId required")
			return
		}

		rt, err := agentrt.NewSobekRuntime()
		if err != nil {
			writeRunErr(w, http.StatusInternalServerError, "create runtime: "+err.Error())
			return
		}
		anyrt.SetupAnySDKDirtyRuntime(rt, anyrt.RuntimeConfig{
			APIBaseURL:     base,
			SpaceID:        bobrikSpaceID,
			PrivateSpaceID: bobrikSpaceID,
			ProgramTypeID:  programTypeID,
			DebugFolderID:  getDebugFolderID(),
		})

		// Args reach the program as both the main(args) param and the `args`
		// global. spaceId is the target the program acts on; apiBaseUrl lets it
		// reach the server for raw reads (transcript markdown, type catalog).
		args := map[string]any{}
		maps.Copy(args, req.Args)
		if req.SpaceID != "" {
			args["spaceId"] = req.SpaceID
		}
		args["apiBaseUrl"] = base

		var name, source string
		switch {
		case req.Method != "":
			// Call a named export with positional args from `call`. Requires
			// the import-by-name path (a named export, not main()).
			if req.Program == "" {
				writeRunErr(w, http.StatusBadRequest, "method requires program (import string)")
				return
			}
			args["__call"] = req.Call
			name = "__run__:" + req.Program + "." + req.Method
			source = fmt.Sprintf(
				"import { %s as __m } from %q;\nexport function main(args){ return __m.apply(null, (args && args.__call) || []); }",
				req.Method, req.Program,
			)
		case req.ProgramID != "":
			name = "__run__:" + req.ProgramID
			source, err = anyrt.QueryProgramSource(base, bobrikSpaceID, req.ProgramID)
			if err != nil {
				writeRunErr(w, http.StatusBadGateway, "read program source: "+err.Error())
				return
			}
			if source == "" {
				writeRunErr(w, http.StatusNotFound, "program "+req.ProgramID+" not found / empty source in space "+bobrikSpaceID)
				return
			}
		default:
			// Import the program by name and forward args to its main().
			name = "__run__:" + req.Program
			source = fmt.Sprintf(
				"import { main as __m } from %q;\nexport function main(args){ return __m(args); }",
				req.Program,
			)
		}

		result, evalErr := rt.EvalToString(name, source, args)
		resp := runResponse{}
		switch {
		case evalErr != nil:
			resp.Error = evalErr.Error()
		case result.Error != "":
			resp.Error = result.Error
		default:
			resp.Result = result.Result
		}
		if req.Trace && result != nil {
			resp.Trace = result.Trace
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
}

func writeRunErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(runResponse{Error: msg})
}
