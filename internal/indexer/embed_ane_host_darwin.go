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

// neuralEngineHost: an Apple Silicon Mac on a new enough macOS. Whether
// it has a usable Neural Engine is the embed child's check.
func neuralEngineHost() bool {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil {
		return false
	}
	major, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(major)
	return err == nil && n >= neuralEngineMinMacOS
}
