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

WEBAPP  := web/app
EMBED   := internal/server/webapp/dist

.PHONY: build test vet tidy clean web web-clean web-install

build: web
	@mkdir -p $(OUT)
	go build -v -ldflags '$(LDFLAGS)' -o $(OUT)/$(BINARY) ./cmd/any

# Build the SPA, then mirror its dist/ into the directory the Go
# binary embeds via //go:embed.
web: web-install
	cd $(WEBAPP) && pnpm build
	rm -rf $(EMBED)
	mkdir -p $(EMBED)
	cp -r $(WEBAPP)/dist/. $(EMBED)/
	@echo "" > $(EMBED)/.gitkeep

# Install SPA deps idempotently. CI uses --frozen-lockfile.
web-install:
	@if [ ! -d $(WEBAPP)/node_modules ]; then \
		cd $(WEBAPP) && pnpm install --frozen-lockfile; \
	fi

web-clean:
	rm -rf $(WEBAPP)/dist $(WEBAPP)/node_modules
	rm -rf $(EMBED)
	mkdir -p $(EMBED)
	@echo "" > $(EMBED)/.gitkeep

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean: web-clean
	rm -rf $(OUT)
