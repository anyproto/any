//go:build llamacpp && !android && !ios && !(darwin && arm64)

package indexer

// neuralEngineHost: only an Apple Silicon Mac has a Neural Engine.
func neuralEngineHost() bool { return false }

// aneHostSetup: no chip and OS build to name.
func aneHostSetup() string { return "" }
