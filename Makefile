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

SWAG := deps/swag

export PATH := $(CURDIR)/deps:$(PATH)

.PHONY: build test vet tidy clean swagger deps

deps:
	@mkdir -p deps
	go build -o deps/ github.com/swaggo/swag/cmd/swag

swagger:
	$(SWAG) init -g doc.go -d ./internal/server,./internal/api -o internal/server/docs --parseDependency --parseInternal

build: swagger
	@mkdir -p $(OUT)
	go build -v -ldflags '$(LDFLAGS)' -o $(OUT)/$(BINARY) ./cmd/any
	go build -v -o $(OUT)/bobrik-watch $(PKG)/cmd/bobrik-watch

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy

clean:
	rm -rf $(OUT) dist

include makefiles/android.mk
