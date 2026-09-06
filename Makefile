BINARY  := any
PKG     := github.com/anyproto/any
OUT     := ./bin

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.BuildDate=$(DATE)

SWAG := go tool github.com/swaggo/swag/v2/cmd/swag

# Search-index build tags (docs/13-index.md § build tags). The desktop
# binary ships both legs; each is independently selectable so other
# builds (e.g. gomobile) can drop one or both — `fts` compiles in the
# BM25 full-text leg, `vector` the embedding + IVF-SQ ANN leg (and the
# embedder implementations, including the llama.cpp bindings). Build/test
# with neither to exclude the whole search index from compilation.
INDEX_TAGS := fts vector

# llama.cpp release pin for the local embedder's shared libs
# (docs/13-index.md § local embedder). Bump together with the yzma
# dependency — yzma tracks llama.cpp releases, and only a matching pair
# works: too old a build fails to load ("undefined symbol"), too new a
# one loads but reads shifted struct fields (llama_model_n_embd comes
# back 0 and context creation aborts). yzma's README carries the
# compatibility table; verify a bump by embedding with the real model.
LLAMACPP_VERSION := b10620

.PHONY: build test vet tidy clean swagger llamacpp llamacpp-soft any docs docs-serve catalog-validate

swagger:
	$(SWAG) init --v3.1 -g doc.go -d ./internal/server,./internal/api -o internal/server/docs --parseDependency --parseInternal

build: swagger llamacpp-soft
	@mkdir -p $(OUT)
	go build -v -tags '$(INDEX_TAGS)' -ldflags '$(LDFLAGS)' -o $(OUT)/$(BINARY) ./cmd/any

# Prebuilt llama.cpp shared libs for `index.embedder: local` — fetched
# once into bin/llamacpp/ (cached tarball under third_party/llamacpp/);
# a no-op once bin/llamacpp/VERSION matches the pin, so repeat builds
# stay network-free. GPU-capable bundles with automatic CPU fallback
# (Metal on macOS arm64, Vulkan on Linux/Windows) —
# docs/13-index.md § GPU offload.
llamacpp:
	./scripts/fetch-llamacpp.sh $(LLAMACPP_VERSION) $(OUT)/llamacpp

# `build` prerequisite: same fetch, but failure-tolerant — on an
# unsupported platform or without network the build still succeeds and
# the local embedder keeps retrying at runtime (docs/13-index.md).
llamacpp-soft:
	@./scripts/fetch-llamacpp.sh $(LLAMACPP_VERSION) $(OUT)/llamacpp \
		|| echo "make: llamacpp libs unavailable — 'index.embedder: local' won't work until 'make llamacpp' succeeds or index.local.libDir is set" >&2

test:
	go test -tags '$(INDEX_TAGS)' ./...

vet:
	go vet -tags '$(INDEX_TAGS)' ./...

# Validate the embedded usecase catalog (internal/catalog/catalog.yml)
# and any candidate files passed as FILES — every problem with its
# yaml path, exit 1 on any. Offline: no server, no space. CI runs it on
# every PR and before every release build (docs/18-ci.md).
catalog-validate:
	go run ./internal/catalog/cmd/catalog-validate $(FILES)

tidy:
	go mod tidy

clean:
	rm -rf $(OUT) dist

# Build one any payload (host platform by default) into dist/.
# Override with PLATFORM=darwin-arm64|darwin-x64|linux-x86_64|windows-x86_64,
# optionally with a -sandbox suffix on the darwin ones (docs/18-ci.md).
any:
	scripts/build-any.sh $${PLATFORM:-host} dist

include makefiles/android.mk

## Docs website: website/*.md -> website/dist (static, deploy as-is)
docs:
	go run ./cmd/anydocs -src website -out website/dist

docs-serve: docs
	@echo "http://0.0.0.0:8088/"; cd website/dist && python3 -m http.server 8088 --bind 0.0.0.0
