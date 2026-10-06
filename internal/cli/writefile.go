package cli

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// writeFile streams r into path so that a failed transfer never
// destroys what was there: an existing regular file is replaced only on
// success, through a sibling temp file renamed over it, and a file this
// call created is removed on failure. A device or pipe is written in
// place.
func writeFile(path string, r io.Reader) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, err
	}
	if err == nil && fi.Mode().IsRegular() {
		return replaceFile(path, fi.Mode().Perm(), r)
	}
	created := err != nil
	flag := os.O_WRONLY
	if created {
		flag |= os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flag, 0o666)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil && created {
		_ = os.Remove(path)
	}
	return n, err
}

func replaceFile(path string, perm fs.FileMode, r io.Reader) (int64, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(tmp, r)
	if err == nil {
		err = tmp.Chmod(perm)
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
