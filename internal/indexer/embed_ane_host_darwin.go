//go:build llamacpp && darwin && arm64 && !ios

package indexer

import (
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// neuralEngineMinMacOS is the first macOS whose Core ML loads one
// function of a multi-function model.
const neuralEngineMinMacOS = 15

// neuralEngineHost: an Apple Silicon Mac, not a virtual one, on a new
// enough macOS. Whether its Neural Engine is usable is the embed
// child's check.
func neuralEngineHost() bool {
	// A virtual machine has no Neural Engine to pass through.
	if vm, err := unix.SysctlUint32("kern.hv_vmm_present"); err == nil && vm != 0 {
		return false
	}
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return false
	}
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= neuralEngineMinMacOS
}

// aneHostSetup names what Core ML's placement of a model depends on
// besides the model: the chip and the OS build. "" = unknown.
func aneHostSetup() string {
	chip, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil || chip == "" {
		return ""
	}
	build, err := unix.Sysctl("kern.osversion")
	if err != nil || build == "" {
		return ""
	}
	return chip + ", macOS build " + build
}
