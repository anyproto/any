//go:build vector && !gomobile

package indexer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/utils"
)

// llamaLog keeps the hardware lines llama.cpp emits while backends
// register and the model loads. Everything else is dropped — the log
// callback stays effectively silent, which is what the server wants on
// its own stderr.
var llamaLog struct {
	sync.Mutex
	lines []string
}

// hardwareLinePrefixes are the log lines worth keeping: which backend
// came from which shared object, and what devices it found. One prefix
// per backend family, because each prints its own enumeration.
var hardwareLinePrefixes = []string{
	"load_backend:",
	"ggml_vulkan:",
	"ggml_metal",
	"ggml_cuda",
	"ggml_sycl",
	"ggml_opencl",
}

// llamaLogCapture is the llama_log_set callback: it keeps recognized
// hardware lines and discards the rest. Called from llama.cpp threads.
func llamaLogCapture() uintptr {
	return purego.NewCallback(func(level int32, text unsafe.Pointer, data uintptr) uintptr {
		if text == nil {
			return 0
		}
		line := strings.TrimSpace(utils.BytePtrToString((*byte)(text)))
		if line == "" || !hardwareLine(line) {
			return 0
		}
		llamaLog.Lock()
		defer llamaLog.Unlock()
		if len(llamaLog.lines) < 64 { // bounded: enumeration is short, decoding is silent
			llamaLog.lines = append(llamaLog.lines, line)
		}
		return 0
	})
}

func hardwareLine(line string) bool {
	for _, p := range hardwareLinePrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

func capturedHardwareLines() []string {
	llamaLog.Lock()
	defer llamaLog.Unlock()
	return append([]string(nil), llamaLog.lines...)
}

// Hardware reports what this embedder is running on. Call it after the
// model is loaded: PrintSystemInfo walks the registered backends, so it
// needs them initialized.
func (l *Local) Hardware() Hardware {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := Hardware{
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		CPUs:       runtime.NumCPU(),
		Threads:    l.threadCount(),
		GpuLayers:  l.gpuLayers,
		LibDir:     l.libDir,
		LibVersion: libVersionStamp(l.libDir),
		SystemInfo: strings.TrimSpace(llama.PrintSystemInfo()),
	}
	for _, line := range capturedHardwareLines() {
		switch {
		case strings.HasPrefix(line, "load_backend:"):
			// "load_backend: loaded Vulkan backend from /path/libggml-vulkan.so"
			name, path, ok := strings.Cut(strings.TrimPrefix(line, "load_backend: loaded "), " backend from ")
			if !ok {
				continue
			}
			h.Backends = append(h.Backends, name+" from "+filepath.Base(strings.TrimSpace(path)))
		default:
			if d := deviceDescription(line); d != "" {
				h.Devices = append(h.Devices, d)
			}
		}
	}
	return h
}

// deviceDescription pulls the device out of an enumeration line, or
// returns "" for the capability lines that share the same prefixes.
// Vulkan and CUDA number their devices ("ggml_vulkan: 0 = AMD ...",
// "  Device 0: NVIDIA ..."); Metal names its one device on a labelled
// line and then logs dozens of "<capability> = true" lines, which is
// why a bare " = " is not enough to go on.
func deviceDescription(line string) string {
	for _, label := range []string{"GPU name:", "picking default device:", "deviceDescription:"} {
		if _, desc, ok := strings.Cut(line, label); ok {
			return strings.TrimSpace(desc)
		}
	}
	head, desc, ok := strings.Cut(line, " = ")
	if !ok {
		return ""
	}
	// The head must end in a device index: "ggml_vulkan: 0", "Device 1".
	_, num, ok := strings.Cut(head, ": ")
	if !ok {
		return ""
	}
	if num = strings.TrimSpace(num); num == "" {
		return ""
	}
	for _, r := range num {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return strings.TrimSpace(desc)
}

// libVersionStamp reads the VERSION file fetch-llamacpp.sh writes next
// to the shared libs ("b10620-ubuntu-vulkan-x64"). Absent for a custom
// build pointed at by index.local.libDir.
func libVersionStamp(libDir string) string {
	if libDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(libDir, "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
