package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile streams r into path so that a failed transfer never
// destroys what was there. An existing regular file is opened first,
// so the usual permission checks and symlinks apply, and is
// overwritten in place only once r is fully staged in a temp file: it
// keeps its inode, links, owner and mode. A file this call created is
// removed on failure. A device or pipe is written directly.
func writeFile(path string, r io.Reader) (n int64, err error) {
	_, err = os.Lstat(path)
	created := errors.Is(err, fs.ErrNotExist)
	if err != nil && !created {
		return 0, err
	}
	flag := os.O_WRONLY | os.O_CREATE
	if created {
		flag |= os.O_EXCL
	}
	f, err := os.OpenFile(path, flag, 0o666)
	if err != nil {
		return 0, err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil && created {
			_ = os.Remove(path)
		}
	}()
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if created || !fi.Mode().IsRegular() {
		return io.Copy(f, r)
	}
	return overwrite(f, path, r)
}

// overwrite stages r in a temp file next to path, or in the system temp
// dir when that fails, then copies it over f.
func overwrite(f *os.File, path string, r io.Reader) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		if tmp, err = os.CreateTemp("", "any-transfer-*"); err != nil {
			return 0, err
		}
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	n, err := io.Copy(tmp, r)
	if err != nil {
		return n, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return n, err
	}
	if err := f.Truncate(0); err != nil {
		return n, err
	}
	_, err = io.Copy(f, tmp)
	return n, err
}
