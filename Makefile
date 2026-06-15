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

SWAG := go tool swag

# llama.cpp release pin for the local embedder's shared libs
# (docs/13-index.md § local embedder). Bump together with the yzma
# dependency — yzma tracks llama.cpp releases.
LLAMACPP_VERSION := b9590

.PHONY: build test vet tidy clean swagger llamacpp llamacpp-soft sdk

swagger:
	$(SWAG) init -g doc.go -d ./internal/server,./internal/api -o internal/server/docs --parseDependency --parseInternal

build: swagger llamacpp-soft
	@mkdir -p $(OUT)
	go build -v -ldflags '$(LDFLAGS)' -o $(OUT)/$(BINARY) ./cmd/any
	go build -v -o $(OUT)/bobrik-watch $(PKG)/cmd/bobrik-watch
	go build -v -o $(OUT)/any-agent-runtime $(PKG)/cmd/any-agent-runtime

# Prebuilt llama.cpp shared libs for `index.embedder: local` — fetched
# once into bin/llamacpp/ (cached tarball under third_party/llamacpp/);
# a no-op once bin/llamacpp/VERSION matches the pin, so repeat builds
# stay network-free.
llamacpp:
	./scripts/fetch-llamacpp.sh $(LLAMACPP_VERSION) $(OUT)/llamacpp

# `build` prerequisite: same fetch, but failure-tolerant — on an
# unsupported platform or without network the build still succeeds and
# the local embedder keeps retrying at runtime (docs/13-index.md).
llamacpp-soft:
	@./scripts/fetch-llamacpp.sh $(LLAMACPP_VERSION) $(OUT)/llamacpp \
		|| echo "make: llamacpp libs unavailable — 'index.embedder: local' won't work until 'make llamacpp' succeeds or index.local.libDir is set" >&2

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(OUT)

# Build one any-sdk payload (host platform by default) into dist/.
# Override with PLATFORM=darwin-arm64|darwin-x64|linux-x86_64|windows-x86_64.
sdk:
	scripts/build-sdk.sh $${PLATFORM:-host} dist
