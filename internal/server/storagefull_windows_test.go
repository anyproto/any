//go:build windows

package server

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDiskFull_Windows(t *testing.T) {
	for _, e := range []error{windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL} {
		if !diskFull(&os.PathError{Op: "write", Path: "x", Err: e}) {
			t.Fatalf("diskFull(%v) = false", e)
		}
	}
	if diskFull(&os.PathError{Op: "write", Path: "x", Err: windows.ERROR_ACCESS_DENIED}) {
		t.Fatal("diskFull(ERROR_ACCESS_DENIED) = true")
	}
}
