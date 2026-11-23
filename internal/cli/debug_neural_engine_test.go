package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// The first interrupt cancels the run and the second ends the process;
// once the run is cleaned up, as when its output blocks on a full pipe,
// the first ends it at once.
func TestExitOnSecondInterrupt(t *testing.T) {
	if mode := os.Getenv("ANY_TEST_INTERRUPT_CLEANED"); mode != "" {
		interruptedChild(mode == "true")
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("no signal to send")
	}
	for _, cleaned := range []bool{false, true} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestExitOnSecondInterrupt$")
		cmd.Env = append(os.Environ(), "ANY_TEST_INTERRUPT_CLEANED="+strconv.FormatBool(cleaned))
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines := make(chan string)
		go func() {
			defer close(lines)
			for s := bufio.NewScanner(stdout); s.Scan(); {
				lines <- s.Text()
			}
		}()
		exited := make(chan error, 1)
		next := func(want string) {
			t.Helper()
			select {
			case got := <-lines:
				if got != want {
					t.Fatalf("cleaned %v: the child said %q, want %q", cleaned, got, want)
				}
			case <-time.After(time.Minute):
				_ = cmd.Process.Kill()
				t.Fatalf("cleaned %v: the child did not say %q", cleaned, want)
			}
		}
		next("ready")
		if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		if !cleaned {
			next("cancelled")
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
		}
		go func() {
			for range lines {
			}
			exited <- cmd.Wait()
		}()
		select {
		case <-exited:
		case <-time.After(time.Minute):
			_ = cmd.Process.Kill()
			<-exited
			t.Fatalf("cleaned %v: the child did not exit", cleaned)
		}
		if code := cmd.ProcessState.ExitCode(); code != 128+int(syscall.SIGTERM) {
			t.Fatalf("cleaned %v: exit %d, want %d", cleaned, code, 128+int(syscall.SIGTERM))
		}
	}
}

// interruptedChild waits for interrupts as the command does, and once
// cancelled stays blocked, as a write to a full pipe would.
func interruptedChild(cleaned bool) {
	ctx, cancel := context.WithCancel(context.Background())
	cleanedUp := exitOnSecondInterrupt(cancel)
	if cleaned {
		cleanedUp()
	}
	fmt.Println("ready")
	<-ctx.Done()
	fmt.Println("cancelled")
	time.Sleep(time.Minute)
	os.Exit(0)
}

// A config that fails to load is an error that exits 1, also when the
// reader of stderr went away: the write fails instead of a SIGPIPE
// killing the process.
func TestDebugNeuralEngine_ConfigErrorToAClosedPipe(t *testing.T) {
	if missing := os.Getenv("ANY_TEST_MISSING_CONFIG"); missing != "" {
		os.Args = []string{"any", "debug", "neural-engine", "--config", missing}
		os.Exit(Execute())
	}
	if runtime.GOOS == "windows" {
		t.Skip("no SIGPIPE")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	defer w.Close()
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDebugNeuralEngine_ConfigErrorToAClosedPipe$")
	cmd.Env = append(os.Environ(), "ANY_TEST_MISSING_CONFIG="+filepath.Join(dir, "missing.yaml"), "ANY_DATA_DIR="+dir)
	cmd.Stdout, cmd.Stderr = w, w
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("%v, want exit 1", cmd.ProcessState)
	}
}
