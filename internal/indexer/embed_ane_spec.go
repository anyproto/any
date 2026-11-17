//go:build llamacpp && !android && !ios

package indexer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

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
// the models dir, one after another, then its licence texts. The package
// is ready when every file of it is in place; the embed child looks for
// that itself.
type packageDownload struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// noticeFetchTimeout is how long one licence text may take. They are
// small: past it the fetch is left for the next start (a var so tests
// can change it).
var noticeFetchTimeout = 30 * time.Second

// startPackageDownload spawns the download. It begins once after, the
// model's own download, has installed its file (nil = nothing to wait
// for): one process row serves every model download, one at a time.
func startPackageDownload(pkg *coreMLPackage, modelsDir string, after *modelDownload, client *http.Client, onProcess func(ProcessUpdate)) *packageDownload {
	ctx, cancel := context.WithCancel(context.Background())
	p := &packageDownload{cancel: cancel, done: make(chan struct{})}
	// fetch installs one file and reports whether it is in place. The
	// .part and .lock stay out of the directory the file goes into,
	// under the name the process row carries.
	fetch := func(ctx context.Context, f coreMLFile, url, dir string, onProcess func(ProcessUpdate)) bool {
		name := pkg.id() + "." + path.Base(f.path)
		dest := filepath.Join(dir, filepath.FromSlash(f.path))
		d := startModelDownloadAt(name, url+"/"+f.path, dest, filepath.Join(modelsDir, name), f.sha256, client, onProcess)
		select {
		case <-d.finished:
		case <-ctx.Done():
			d.Close()
		}
		return d.Status() == nil
	}
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
		dir := pkg.dir(modelsDir)
		for _, f := range pkg.files {
			if (coreMLFile{path: f.path}).verify(dir) == nil {
				continue
			}
			if !fetch(ctx, f, pkg.url, dir, onProcess) {
				return // cancelled, or stopped on its checksum: the rest cannot complete the package
			}
		}
		// The licence texts follow the package and never hold it back.
		// They are a few kilobytes and report no process.
		if pkg.noticesURL == "" {
			return
		}
		notices := pkg.noticesDir(modelsDir)
		for _, f := range pkg.notices {
			if ctx.Err() != nil {
				return
			}
			err := f.verify(notices)
			if err == nil {
				continue
			}
			// A text of another revision is replaced; a missing one may
			// be another process's install in flight and is left alone.
			if !errors.Is(err, fs.ErrNotExist) {
				_ = os.Remove(filepath.Join(notices, filepath.FromSlash(f.path)))
			}
			one, stop := context.WithTimeout(ctx, noticeFetchTimeout)
			fetch(one, f, pkg.noticesURL, notices, nil)
			stop()
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
