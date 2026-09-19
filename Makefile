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

# `llamacpp` compiles in the local llama.cpp embedder (docs/13-index.md
# § Builds and the local embedder). Search itself needs no tag; a build
# without it embeds through the configured online provider, or not at all.
BUILD_TAGS := llamacpp

# llama.cpp release pin for the local embedder's shared libs; the source
# of truth and its bump rules live in internal/indexer/llamacpp_release.go.
LLAMACPP_VERSION := $(shell scripts/llamacpp-version.sh)

# Packages with llamacpp-constrained files: `make test` reruns them with
# the tag after the untagged suite, which is the `go install` build.
LLAMACPP_PKGS := $(addprefix ./,$(sort $(dir $(shell grep -rl --include='*.go' '^//go:build.*llamacpp' internal cmd mobile))))

.PHONY: build test vet tidy clean swagger llamacpp llamacpp-soft any docs docs-check docs-serve catalog-validate check-deps

swagger:
	$(SWAG) init --v3.1 -g doc.go -d ./internal/server,./internal/api -o internal/server/docs --parseDependency --parseInternal --overridesFile $(CURDIR)/.swaggo

build: swagger llamacpp-soft
	@mkdir -p $(OUT)
	go build -v -tags '$(BUILD_TAGS)' -ldflags '$(LDFLAGS)' -o $(OUT)/$(BINARY) ./cmd/any

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
	go test ./...
	go test -tags '$(BUILD_TAGS)' $(LLAMACPP_PKGS)

vet:
	go vet -tags '$(BUILD_TAGS)' ./...

# The untagged build (`go install`) and the mobile targets must never
# link the llama.cpp bindings: their ffi dependency loads libffi at
# process start and panics without it (docs/13-index.md § Builds and the
# local embedder). The mobile checks pass `llamacpp` to prove the GOOS
# exclusion holds. A failing `go list` fails the check too.
FFI_PKG := github.com/jupiterrider/ffi
# $(call no-ffi,label,env,tags,package)
no-ffi = deps=$$($(2) go list -deps -tags '$(3)' $(4)) && ! printf '%s\n' "$$deps" | grep -qx '$(FFI_PKG)' \
	|| { echo "check-deps: $(1): go list failed or it links $(FFI_PKG)" >&2; exit 1; }
check-deps:
	@$(call no-ffi,untagged ./cmd/any,,,./cmd/any)
	@$(call no-ffi,android,GOOS=android GOARCH=arm64 CGO_ENABLED=1,gomobile llamacpp,./mobile/android)
	@$(call no-ffi,ios,GOOS=ios GOARCH=arm64 CGO_ENABLED=1,mobile llamacpp,./mobile/ios)

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

docs-check: docs
	go vet ./cmd/anydocs
	go test ./cmd/anydocs
	python3 cmd/anydocs/check_site.py website/dist
	node --check website/dist/assets/examples/client.mjs
	node --test cmd/anydocs/client_js_test.mjs
	python3 cmd/anydocs/client_python_test.py website/dist/assets/examples/client.py

docs-serve: docs
	@echo "http://0.0.0.0:8088/"; cd website/dist && python3 -m http.server 8088 --bind 0.0.0.0
