package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var errCut = errors.New("transfer cut")

// cutReader yields its data, then fails.
type cutReader struct{ r io.Reader }

func (c *cutReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, errCut
	}
	return n, err
}

func TestWriteFile_FailureKeepsExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := writeFile(path, &cutReader{strings.NewReader("partial")}); !errors.Is(err, errCut) {
		t.Fatalf("err = %v, want the read error", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "previous" {
		t.Fatalf("content = %q, want the previous file untouched", b)
	}
	assertOnlyEntry(t, dir, "out.bin")
}

func TestWriteFile_ReplacesExistingFileKeepingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(path, []byte("previous, longer"), 0o640); err != nil {
		t.Fatal(err)
	}

	n, err := writeFile(path, strings.NewReader("new"))
	if err != nil || n != 3 {
		t.Fatalf("writeFile = %d, %v", n, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("content = %q, want new", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
			t.Fatalf("mode = %v, want 0640 kept", fi.Mode().Perm())
		}
	}
	assertOnlyEntry(t, dir, "out.bin")
}

func TestWriteFile_FailureLeavesNoNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")

	if _, err := writeFile(path, &cutReader{strings.NewReader("partial")}); !errors.Is(err, errCut) {
		t.Fatalf("err = %v, want the read error", err)
	}
	assertOnlyEntry(t, dir, "")
}

// assertOnlyEntry fails unless dir holds exactly name (nothing when
// name is empty), so no temp file is left behind.
func assertOnlyEntry(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if name == "" && len(names) == 0 || len(names) == 1 && names[0] == name {
		return
	}
	t.Fatalf("dir entries = %v, want only %q", names, name)
}
