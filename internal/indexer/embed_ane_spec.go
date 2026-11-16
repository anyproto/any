//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"

	"github.com/anyproto/any/internal/config"
)

// aneHost reports whether this machine can run the Neural Engine path.
// A var so tests can stand in for a Mac.
var aneHost = neuralEngineHost

// planNeuralEngine decides whether the embed child gets the model's Core
// ML package; nil is llama.cpp alone. It starts nothing: a refused
// config must not leave a download running.
func planNeuralEngine(cfg config.IndexLocal, modelsDir string) (*coreMLPackage, error) {
	mode := cfg.NeuralEngine
	switch mode {
	case "", config.NeuralEngineAuto, config.NeuralEngineOn:
	case config.NeuralEngineOff:
		return nil, nil
	default:
		return nil, fmt.Errorf("indexer: unknown index.local.neuralEngine %q (%s, %s, %s)",
			mode, config.NeuralEngineAuto, config.NeuralEngineOn, config.NeuralEngineOff)
	}
	// Auto runs without it; on refuses to.
	without := func(why string) (*coreMLPackage, error) {
		if mode == config.NeuralEngineOn {
			return nil, fmt.Errorf("indexer: index.local.neuralEngine: on: %s", why)
		}
		return nil, nil
	}
	if !aneHost() {
		return without("it needs an Apple Silicon Mac, not a virtual machine, on macOS 15 or later")
	}
	model, err := lookupLocalModel(cfg.Model)
	if err != nil {
		return nil, nil // resolveLocalSpec reports the unknown model
	}
	named := cfg.Model != "" || cfg.ModelPath == ""
	if !named || model.coreml == nil {
		return without("the model has no Core ML package (" + config.LocalModelEmbeddingGemma2 + " has one)")
	}
	// The package pools the way the model does.
	if cfg.Pooling != "" && cfg.Pooling != model.pooling {
		return without("index.local.pooling is not the model's own")
	}
	pkg := model.coreml
	if !pkg.complete(pkg.dir(modelsDir)) && (cfg.ModelPath != "" || pkg.url == "") {
		return without("the model's Core ML package is not in the models directory and is not downloaded")
	}
	return pkg, nil
}

// packageDownload fetches the missing files of a Core ML package into
// the models dir, one after another. The package is ready when every
// file is in place; the embed child looks for that itself.
type packageDownload struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// startPackageDownload spawns the download. It begins once after, the
// model's own download, has installed its file (nil = nothing to wait
// for): one process row serves every model download, one at a time.
func startPackageDownload(pkg *coreMLPackage, modelsDir string, after *modelDownload, client *http.Client, onProcess func(ProcessUpdate)) *packageDownload {
	ctx, cancel := context.WithCancel(context.Background())
	p := &packageDownload{cancel: cancel, done: make(chan struct{})}
	dir := pkg.dir(modelsDir)
	go func() {
		defer close(p.done)
		if after != nil {
			select {
			case <-after.finished:
			case <-ctx.Done():
				return
			}
			if after.Status() != nil {
				return // no model, nothing for the package to run next to
			}
		}
		for _, f := range pkg.files {
			dest := filepath.Join(dir, filepath.FromSlash(f.path))
			if _, err := os.Stat(dest); err == nil {
				continue
			}
			// The .part and .lock stay out of the package directory,
			// under the name the process row carries.
			name := pkg.name + "." + path.Base(f.path)
			d := startModelDownloadAt(name, pkg.url+"/"+f.path, dest, filepath.Join(modelsDir, name), f.sha256, client, onProcess)
			select {
			case <-d.finished:
			case <-ctx.Done():
				d.Close()
				return
			}
			if d.Status() != nil {
				return // stopped on its checksum; the rest cannot complete the package
			}
		}
	}()
	return p
}

// Close stops the download and waits for it. Finished files and the
// .part stay for the next start.
func (p *packageDownload) Close() {
	p.cancel()
	<-p.done
}
