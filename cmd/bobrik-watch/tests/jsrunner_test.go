//go:build integration

// JS-level integration tests for anyHelper.
package tests

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const harnessMarker = "HARNESS_RESULT "

// jsHarnessSummary mirrors the JSON the JS harness prints.
type jsHarnessSummary struct {
	Pass     int      `json:"pass"`
	Fail     int      `json:"fail"`
	Failures []string `json:"failures"`
}

// bobrikDir returns the absolute path to cmd/bobrik-watch (the -m module-
// resolution root that holds anyHelper.js). The Go test runs with cwd set to
// this package dir (cmd/bobrik-watch/tests), so the parent is the target.
func bobrikDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve bobrik dir: %v", err)
	}
	return abs
}

// writeRuntimeEnv writes the .env the runtime expects (it reads API config from
// a dotfile, not OS env). Module resolution tries the Anytype API first, so we
// point it at the same server; lookups for `anyHelper@v1` miss there and fall
// back to the -m local dir. No auth on localhost, so the key is empty.
func writeRuntimeEnv(t *testing.T, spaceID string) string {
	t.Helper()
	env := "ANYTYPE_API_URL=" + baseURL() + "\nANYTYPE_API_KEY=\nANYTYPE_SPACE_ID=" + spaceID + "\n"
	path := filepath.Join(t.TempDir(), "runtime.env")
	if err := os.WriteFile(path, []byte(env), 0o600); err != nil {
		t.Fatalf("write runtime env: %v", err)
	}
	return path
}

// runJSTest execs the runtime on one JS file and returns the parsed summary
// plus the raw combined output (for diagnostics on failure).
func runJSTest(t *testing.T, runtime, envPath, dir, spaceID, file string) (jsHarnessSummary, string) {
	t.Helper()
	cmd := exec.Command(runtime,
		"-e", envPath,
		"-m", dir,
		file,
		"apiBaseUrl="+baseURL(),
		"spaceId="+spaceID,
	)
	out, err := cmd.CombinedOutput()
	raw := string(out)
	// A non-zero exit is itself a failure (uncaught throw in main, etc.); we
	// still try to parse a summary line in case the harness printed one first.
	summary, ok := parseHarnessResult(t, raw)
	if !ok {
		t.Fatalf("no %q line in runtime output (exit err: %v)\n--- output ---\n%s",
			strings.TrimSpace(harnessMarker), err, raw)
	}
	return summary, raw
}

// parseHarnessResult scans output for the last HARNESS_RESULT line and decodes
// it. Returns ok=false when no such line exists.
func parseHarnessResult(t *testing.T, output string) (jsHarnessSummary, bool) {
	t.Helper()
	var found string
	for _, line := range strings.Split(output, "\n") {
		if i := strings.Index(line, harnessMarker); i >= 0 {
			found = line[i+len(harnessMarker):]
		}
	}
	if found == "" {
		return jsHarnessSummary{}, false
	}
	var s jsHarnessSummary
	if err := json.Unmarshal([]byte(strings.TrimSpace(found)), &s); err != nil {
		t.Fatalf("decode harness result %q: %v", found, err)
	}
	return s, true
}

func TestJSAnyHelper(t *testing.T) {
	// Ensure the server is reachable (doJSON t.Skips if not) and a space exists.
	spaceID := ensureSpace(t)

	runtime, err := exec.LookPath("anytype-agent-runtime")
	if err != nil {
		t.Skipf("anytype-agent-runtime not on PATH (%v) — `go install ./...` in the runtime repo", err)
	}

	dir := bobrikDir(t)
	envPath := writeRuntimeEnv(t, spaceID)

	files, err := filepath.Glob(filepath.Join(dir, "tests", "js", "*_test.js"))
	if err != nil {
		t.Fatalf("glob js tests: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no JS test files found under tests/js/")
	}

	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".js")
		t.Run(name, func(t *testing.T) {
			summary, raw := runJSTest(t, runtime, envPath, dir, spaceID, file)
			if summary.Pass == 0 && summary.Fail == 0 {
				t.Fatalf("harness ran no assertions\n--- output ---\n%s", raw)
			}
			if summary.Fail > 0 {
				t.Errorf("%d/%d JS assertions failed:\n  - %s",
					summary.Fail, summary.Pass+summary.Fail,
					strings.Join(summary.Failures, "\n  - "))
			} else {
				t.Logf("%d JS assertions passed", summary.Pass)
			}
		})
	}
}
