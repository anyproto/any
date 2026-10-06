package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile streams r into path so that a failed transfer never
// destroys what was there. An existing regular file must be writable
// and is replaced only once r is complete: the body goes to a temp file
// next to it (next to a symlink's target), which is synced and renamed
// over it. The replacement keeps the mode but is a new file, so hard
// links keep the old content. When no temp file can be created there,
// the file is overwritten in place. A file this call created is removed
// on failure; a device, a pipe or a dangling symlink's target is
// written directly.
func writeFile(path string, r io.Reader) (int64, error) {
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return writeNew(path, r)
	} else if err != nil {
		return 0, err
	}
	fi, err := os.Stat(path)
	if err == nil && fi.Mode().IsRegular() {
		return replaceFile(path, fi.Mode().Perm(), r)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o666)
	if err != nil {
		return 0, err
	}
	return copyClose(f, r)
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
	// Opening applies the permission checks a rename would skip, and is
	// the in-place fallback.
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return 0, err
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		if err := f.Truncate(0); err != nil {
			_ = f.Close()
			return 0, err
		}
		return copyClose(f, r)
	}
	_ = f.Close()
	n, err := io.Copy(tmp, r)
	if err == nil {
		_ = tmp.Chmod(perm) // best effort: some filesystems refuse chmod
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return n, err
}

func copyClose(f *os.File, r io.Reader) (int64, error) {
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}
