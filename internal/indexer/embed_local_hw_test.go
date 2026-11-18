//go:build llamacpp && !android && !ios

package indexer

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hybridgroup/yzma/pkg/llama"
)

// Error lines of llama.cpp reach stderr, each under the prefix, and none
// of them reads as the start of a crash report; other levels do not.
func TestForwardLlamaError(t *testing.T) {
	var b bytes.Buffer
	forwardLlamaError(&b, int32(llama.LogLevelError), "ggml_metal_library_init: error compiling source\nfatal error: too many errors emitted\n\n")
	forwardLlamaError(&b, int32(llama.LogLevelWarn), "a warning")
	forwardLlamaError(&b, int32(llama.LogLevelInfo), "load_backend: loaded Metal backend")
	forwardLlamaError(&b, int32(llama.LogLevelContinue), "a continuation")
	want := llamaLogPrefix + "ggml_metal_library_init: error compiling source\n" + llamaLogPrefix + "fatal error: too many errors emitted\n"
	if got := b.String(); got != want {
		t.Fatalf("forwarded %q, want %q", got, want)
	}
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if crashStart(line) {
			t.Errorf("%q reads as a crash report start", line)
		}
	}
}

func TestModelDevice(t *testing.T) {
	for line, want := range map[string]string{
		"llama_model_load_from_file_impl: using device Vulkan0 (AMD Radeon (RADV)) (0000:0b:00.0) - 12288 MiB free": "Vulkan0 (AMD Radeon (RADV)) (0000:0b:00.0)",
		"llama_model_load_from_file_impl: using device MTL0 (Apple M2) (unknown id) - 10922 MiB free":               "MTL0 (Apple M2) (unknown id)",
		// What llama.cpp was asked for, not what it did.
		"load_tensors: offloaded 25/25 layers to GPU":         "",
		"load_tensors: offloading 24 repeating layers to GPU": "",
		"ggml_metal_init: found device: Apple M2":             "",
	} {
		if got, _ := modelDevice(line); got != want {
			t.Errorf("modelDevice(%q) = %q, want %q", line, got, want)
		}
	}
}
