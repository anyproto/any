package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile streams r into path so that a failed transfer leaves an
// existing file as it was. The file must be writable and is replaced
// only once r is complete: the body goes to a temp file next to it
// (next to a symlink's target), which is synced and renamed over it
// with its permission bits, so hard links keep the old content. In a
// directory that takes no new file the file is overwritten in place,
// and when the rename is refused the staged body is copied over it. A
// file this call created, a dangling symlink's target included, is
// removed on failure; a device, pipe or socket is written directly.
func writeFile(path string, r io.Reader) (int64, error) {
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return writeNew(linkTarget(path), r)
	case err != nil:
		return 0, err
	case fi.Mode()&(fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0:
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return 0, err
		}
		return copyClose(f, r)
	}
	return replaceFile(path, fi.Mode().Perm(), r)
}

// renameFile is os.Rename; tests make it refuse.
var renameFile = os.Rename

// linkTarget follows the symlinks at path, a dangling one included, to
// the file a write through it creates.
func linkTarget(path string) string {
	for range 40 {
		target, err := os.Readlink(path)
		if err != nil {
			return path
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return path
}

func writeNew(path string, r io.Reader) (int64, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return 0, err
	}
	n, err := copyClose(f, r)
	if err != nil {
		_ = os.Remove(path)
	}
	return n, err
}

func replaceFile(path string, perm fs.FileMode, r io.Reader) (int64, error) {
	// Opening applies the permission checks a rename would skip.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return overwrite(f, r)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".any-transfer-*")
	if errors.Is(err, fs.ErrPermission) {
		return overwrite(f, r)
	}
	// Closed before the rename: Windows refuses to replace an open file.
	_ = f.Close()
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, r)
	if err == nil {
		_ = tmp.Chmod(perm) // best effort: some filesystems refuse chmod
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if renameFile(tmp.Name(), target) == nil {
		return n, nil
	}
	// A refused rename (a bind-mounted file, another user's file in a
	// sticky directory) still allows writing the file in place.
	staged, err := os.Open(tmp.Name())
	if err != nil {
		return n, err
	}
	defer staged.Close()
	if f, err = os.OpenFile(path, os.O_WRONLY, 0); err != nil {
		return n, err
	}
	if _, err := overwrite(f, staged); err != nil {
		return n, err
	}
	return n, nil
}

// overwrite truncates f, writes r into it and closes it.
func overwrite(f *os.File, r io.Reader) (int64, error) {
	if err := f.Truncate(0); err != nil {
		_ = f.Close()
		return 0, err
	}
	return copyClose(f, r)
}

func copyClose(f *os.File, r io.Reader) (int64, error) {
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}
