//go:build llamacpp && !android && !ios && windows

package indexer

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// addLibSearchDir puts the llama.cpp lib dir on the DLL search path.
// Windows resolves a DLL's own imports (ggml.dll → ggml-base.dll →
// libomp.dll) by module name and never looks in the importing DLL's
// folder, so loading ggml.dll by full path fails without it.
func addLibSearchDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	return windows.SetDllDirectory(abs)
}
