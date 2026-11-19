//go:build llamacpp && !android && !ios && !(darwin && arm64)

package indexer

// neuralEngineHost: only an Apple Silicon Mac has a Neural Engine.
func neuralEngineHost() bool { return false }

// aneHostSetup: no setup to record a placement for.
func aneHostSetup() string { return "" }
