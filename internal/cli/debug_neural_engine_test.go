package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
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
		// A timeout of its own ends the child should this process die first.
		cmd := exec.Command(os.Args[0], "-test.run=^TestExitOnSecondInterrupt$", "-test.timeout=2m")
		cmd.Env = append(os.Environ(), "ANY_TEST_INTERRUPT_CLEANED="+strconv.FormatBool(cleaned))
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		lines, quit, exited := make(chan string), make(chan struct{}), make(chan struct{})
		go func() {
			defer close(exited)
			for s := bufio.NewScanner(stdout); s.Scan(); {
				select {
				case lines <- s.Text():
				case <-quit: // nobody listens: read on to the end, then Wait
				}
			}
			_ = cmd.Wait()
		}()
		var stopListening sync.Once
		// Whatever ends the test, the child is killed if need be and waited once.
		t.Cleanup(func() {
			stopListening.Do(func() { close(quit) })
			_ = cmd.Process.Kill()
			<-exited
		})
		next := func(want string) {
			t.Helper()
			select {
			case got := <-lines:
				if got != want {
					t.Fatalf("cleaned %v: the child said %q, want %q", cleaned, got, want)
				}
			case <-time.After(time.Minute):
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
		stopListening.Do(func() { close(quit) })
		select {
		case <-exited:
		case <-time.After(time.Minute):
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestDebugNeuralEngine_ConfigErrorToAClosedPipe$", "-test.timeout=1m")
	cmd.Env = append(os.Environ(), "ANY_TEST_MISSING_CONFIG="+filepath.Join(dir, "missing.yaml"), "ANY_DATA_DIR="+dir)
	cmd.Stdout, cmd.Stderr = w, w
	_ = cmd.Run()
	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Fatalf("%v, want exit 1", cmd.ProcessState)
	}
}

// A config that fails to load ends at an interrupt while its error blocks
// on a full pipe, also when the shell started it with SIGINT ignored.
func TestDebugNeuralEngine_ConfigErrorToAFullPipe(t *testing.T) {
	if missing := os.Getenv("ANY_TEST_MISSING_CONFIG_FULL"); missing != "" {
		signal.Ignore(syscall.SIGINT)
		_ = os.NewFile(3, "ready").Close() // the parent interrupts from here
		os.Args = []string{"any", "debug", "neural-engine", "--config", missing}
		os.Exit(Execute())
	}
	if runtime.GOOS == "windows" {
		t.Skip("no signal to send")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Filled and never read: the child's error line blocks.
	if err := w.SetWriteDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(make([]byte, 1<<20)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("filling the pipe: %v", err)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDebugNeuralEngine_ConfigErrorToAFullPipe$", "-test.timeout=1m")
	cmd.Env = append(os.Environ(), "ANY_TEST_MISSING_CONFIG_FULL="+filepath.Join(dir, "missing.yaml"), "ANY_DATA_DIR="+dir)
	cmd.Stdout, cmd.Stderr = w, w
	ready, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ready.Close()
	cmd.ExtraFiles = []*os.File{readyW}
	err = cmd.Start()
	_ = readyW.Close()
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		_ = cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	if err := ready.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := ready.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("the child did not ignore SIGINT: %v", err)
	}
	// Until it exits: one sent before the receiver is in place is ignored.
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(30 * time.Second)
	for running := true; running; {
		select {
		case <-exited:
			running = false
		case <-tick.C:
			_ = cmd.Process.Signal(syscall.SIGINT)
		case <-deadline:
			t.Fatal("the child ignored SIGINT while its error line blocked")
		}
	}
	if code := cmd.ProcessState.ExitCode(); code != 128+int(syscall.SIGINT) {
		t.Fatalf("%v, want exit %d", cmd.ProcessState, 128+int(syscall.SIGINT))
	}
}
