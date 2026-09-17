//go:build vector && !gomobile && !windows

package indexer

// addLibSearchDir is a no-op: ELF and Mach-O libs find their siblings
// through their own rpath ($ORIGIN, @loader_path).
func addLibSearchDir(string) error { return nil }
