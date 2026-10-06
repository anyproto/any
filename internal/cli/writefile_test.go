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

// A symlink stays a link: its target is replaced, and a dangling
// link's target is created.
func TestWriteFile_WritesThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.bin")
	if err := os.WriteFile(target, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(dir, "dangling.bin")
	if err := os.Symlink(filepath.Join(dir, "missing.bin"), dangling); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{link, dangling} {
		if _, err := writeFile(p, strings.NewReader("new")); err != nil {
			t.Fatalf("%s: %v", filepath.Base(p), err)
		}
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s replaced by a regular file", filepath.Base(p))
		}
	}
	for _, p := range []string{target, filepath.Join(dir, "missing.bin")} {
		if b, _ := os.ReadFile(p); string(b) != "new" {
			t.Fatalf("%s = %q, want new", filepath.Base(p), b)
		}
	}
}

// A name too long to extend still stages beside the file, and a failed
// transfer through a dangling symlink removes the target it created.
func TestWriteFile_FailureEdgeCases(t *testing.T) {
	dir := t.TempDir()
	long := filepath.Join(dir, strings.Repeat("a", 250))
	if err := os.WriteFile(long, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFile(long, &cutReader{strings.NewReader("partial")}); !errors.Is(err, errCut) {
		t.Fatalf("long name: err = %v, want the read error", err)
	}
	if b, _ := os.ReadFile(long); string(b) != "previous" {
		t.Fatalf("long name: content = %q, want the previous file untouched", b)
	}
	assertOnlyEntry(t, dir, filepath.Base(long))

	if runtime.GOOS == "windows" {
		return
	}
	dangling := filepath.Join(dir, "dangling.bin")
	missing := filepath.Join(dir, "missing.bin")
	if err := os.Symlink(missing, dangling); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFile(dangling, &cutReader{strings.NewReader("partial")}); !errors.Is(err, errCut) {
		t.Fatalf("dangling: err = %v, want the read error", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dangling: target left behind (%v)", err)
	}
}

// A refused rename (a bind-mounted file, another user's file in a
// sticky directory) copies the staged body over the file in place.
func TestWriteFile_RefusedRenameOverwritesInPlace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(path, []byte("previous, longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	renameFile = func(string, string) error { return os.ErrPermission }
	t.Cleanup(func() { renameFile = os.Rename })

	n, err := writeFile(path, strings.NewReader("new"))
	if err != nil || n != 3 {
		t.Fatalf("writeFile = %d, %v", n, err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("content = %q, want new", b)
	}
	assertOnlyEntry(t, dir, "out.bin")
}

// A writable file in a directory that takes no new file is overwritten
// in place.
func TestWriteFile_UnwritableDirOverwritesInPlace(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permission checks")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	if err := os.WriteFile(path, []byte("previous, longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := writeFile(path, strings.NewReader("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("content = %q, want new", b)
	}
}

func TestWriteFile_RefusesReadOnlyFile(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permission checks")
	}
	path := filepath.Join(t.TempDir(), "out.bin")
	if err := os.WriteFile(path, []byte("previous"), 0o444); err != nil {
		t.Fatal(err)
	}

	if _, err := writeFile(path, strings.NewReader("new")); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("err = %v, want permission denied", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "previous" {
		t.Fatalf("content = %q, want the read-only file untouched", b)
	}
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
