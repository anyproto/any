// any-agent-runtime — execute a JS program via the same engine + module
// resolution bobrik-watch uses in production: imports ("name@version",
// "private:name@v1", "space:name@v1") resolve against a live `any` server
// (program objects, program_source dataset), with an optional local
// directory as fallback for modules not in the space.
//
// Drop-in replacement for the stock anytype-agent-runtime CLI for `any`
// development: same flags (-e/-m/-t), same script/key=value interface, same
// "res:" output — but the anytype-heart loader is replaced by the anySDK
// loader from internal/anyrt, so a saved program is importable immediately
// (no symlinking, no sync step). The JS integration tests
// (cmd/bobrik-watch/tests) run under this binary for exactly that reason.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/anyproto/anytype-agent-runtime/anyruntime"
	agentruntime "github.com/anyproto/anytype-agent-runtime/runtime"

	"github.com/anyproto/any/internal/anyrt"
	"github.com/anyproto/any/internal/program"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `any-agent-runtime — run JavaScript programs against an `+"`any`"+` server.

Usage:
  %s [options] <script.js> [key=value ...]
  %s [options] - [key=value ...]          # read script from stdin

The script must export a main(args) function. Arguments are passed as
key=value pairs (key=@filepath reads the value from a file) and are
available inside main() as properties of the args object.

Module resolution (production semantics, no caching — live reload):
  1. the `+"`any`"+` server: program object by {name, version}, source from
     the program_source dataset. "private:" / "<spaceId>:" prefixes pin
     the space; unqualified imports fall back to the private space.
  2. with -m: <dir>/name@version.js, then <dir>/name.js.

Environment:
  Reads .env from the current directory (or -e file). Recognized:
  ANYTYPE_API_URL (or ANY_API_URL), ANYTYPE_SPACE_ID (or ANY_SPACE_ID),
  ANYTYPE_PRIVATE_SPACE_ID. Extra variables are passed through to the
  JS-visible env map.

Options:
`, os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}
	envFile := flag.String("e", ".env", "`path` to .env file for API configuration")
	modulesDir := flag.String("m", "", "fallback `directory` for module imports not found on the server")
	traceFile := flag.String("t", "", "write trace JSON to `file`")
	flag.Parse()

	dotEnv := anyruntime.LoadDotEnv(*envFile)
	getenv := func(keys ...string) string {
		for _, k := range keys {
			if v := dotEnv[k]; v != "" {
				return v
			}
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
		return ""
	}

	var source []byte
	var scriptName string
	var cliArgs []string
	var err error

	positional := flag.Args()
	if len(positional) == 0 || positional[0] == "-" {
		source, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read stdin: %s\n", err)
			os.Exit(1)
		}
		scriptName = "stdin.js"
		if len(positional) > 1 {
			cliArgs = positional[1:]
		}
	} else {
		source, err = os.ReadFile(positional[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read %s: %s\n", positional[0], err)
			os.Exit(1)
		}
		scriptName = filepath.Base(positional[0])
		cliArgs = positional[1:]
	}

	rt, err := agentruntime.NewSobekRuntime()
	if err != nil {
		fmt.Fprintf(os.Stderr, "create runtime: %s\n", err)
		os.Exit(1)
	}

	var extra []anyruntime.ModuleLoader
	if *modulesDir != "" {
		extra = append(extra, anyruntime.NewFileLoader(*modulesDir))
	}
	anyrt.SetupAnySDKDirtyRuntime(rt, anyrt.RuntimeConfig{
		APIBaseURL:      getenv("ANYTYPE_API_URL", "ANY_API_URL"),
		SpaceID:         getenv("ANYTYPE_SPACE_ID", "ANY_SPACE_ID"),
		PrivateSpaceID:  getenv("ANYTYPE_PRIVATE_SPACE_ID", "ANY_PRIVATE_SPACE_ID"),
		ProgramTypeID:   program.TypeId, // builtin literal "program"
		ChatReplyWriter: os.Stdout,
		ExtraLoaders:    extra,
		ExtraEnv:        dotEnv,
	})

	args := map[string]any{}
	for _, a := range cliArgs {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(v, "@") {
			content, ferr := os.ReadFile(v[1:])
			if ferr != nil {
				fmt.Fprintf(os.Stderr, "failed to read arg file %s: %s\n", v[1:], ferr)
				os.Exit(1)
			}
			args[k] = string(content)
		} else {
			args[k] = v
		}
	}

	res, err := rt.EvalToString(scriptName, string(source), args)
	if err != nil {
		fmt.Printf("error: %s\n", err.Error())
		os.Exit(1)
	}

	if *traceFile != "" {
		traceJSON, _ := json.Marshal(res.Trace)
		if werr := os.WriteFile(*traceFile, traceJSON, 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "failed to write trace file: %s\n", werr)
		}
	}

	fmt.Printf("res: %v\n", res.Result)
	if res.Error != "" {
		fmt.Printf("err: %s\n", res.Error)
	}
	// console.log output lives in the trace — print it like the stock CLI
	// does (the JS test harness greps stdout for its HARNESS_RESULT line).
	anyruntime.PrintTrace(res.Trace)
	if res.Error != "" {
		os.Exit(1)
	}
}
