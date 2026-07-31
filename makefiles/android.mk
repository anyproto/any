GOBIN    := $(shell go env GOPATH)/bin
GOMOBILE := $(GOBIN)/gomobile
GOBIND   := $(GOBIN)/gobind

# `gomobile` selects mobile code paths; `fts` compiles the BM25 search leg
ANY_TAGS := gomobile fts

# Build the go.mod-PINNED gomobile + gobind into GOPATH/bin and initialize
# gomobile. `go build <cmd-pkg>` resolves the commands at the version pinned
# in go.mod (golang.org/x/mobile, held in the module graph by mobile/tools.go's
# `bind` import) — NO `@latest`, so the toolchain can't drift release-to-release.
# `gomobile init` runs here on the build path; the Android job exports the NDK
# env (ANDROID_NDK{,_HOME,_ROOT}) before invoking make so init can locate the
# toolchain. Idempotent: safe to re-run.
.PHONY: setup-gomobile
setup-gomobile:
	@mkdir -p "$(GOBIN)"
	go build -o "$(GOBIN)/" golang.org/x/mobile/cmd/gomobile golang.org/x/mobile/cmd/gobind
	PATH="$(GOBIN):$$PATH" $(GOMOBILE) init

# Produce dist/android/any.aar from the github.com/anyproto/any/mobile
# package. We build a SINGLE ABI (arm64-v8a) via an explicit `-target` — every
# shipping Android device is arm64 (Google Play has required 64-bit since 2019),
# so armeabi-v7a (dead 32-bit ARM) and x86/x86_64 (Intel emulator only) are
# intentionally dropped to keep the AAR small. The consuming APK's
# ndk.abiFilters picks the subset.
# The AAR is version-stamped via `-ldflags '$(LDFLAGS)'`, which carries the
# `-X .../internal/version.{Version,Commit,BuildDate}` values built from the
# inherited $(VERSION)/$(COMMIT)/$(DATE) (top-level Makefile). CI overrides
# the stamp with make COMMAND-LINE vars — `make build-android VERSION=… COMMIT=…
# DATE=…` — the only thing that beats the Makefile's `:=` assignment (env does
# NOT). No `|| true`: a failed bind fails make and never ships a stale/missing
# aar.
.PHONY: build-android
build-android: setup-gomobile
	@mkdir -p dist/android
	@echo "Building any.aar via gomobile bind..."
	PATH="$(GOBIN):$$PATH" $(GOMOBILE) bind \
		-tags "$(ANY_TAGS)" \
		-ldflags '$(LDFLAGS)' \
		-target=android/arm64 \
		-androidapi 26 \
		-javapkg=io.anyproto.any \
		-o dist/android/any.aar \
		github.com/anyproto/any/mobile
	@echo "Built dist/android/any.aar"

# Drop the freshly-built AAR into a sibling any-kotlin checkout and
# bump the SHA-keyed version in its Gradle config so the change is picked
# up on the next `gradle :libs:publishToMavenLocal`.
#
# Required: CLIENT_ANDROID_PATH=/path/to/any-kotlin-or-bin-to-apk
.PHONY: install-dev-android
install-dev-android: build-android
ifndef CLIENT_ANDROID_PATH
	$(error CLIENT_ANDROID_PATH must point to the any-kotlin checkout)
endif
	@cp dist/android/any.aar $(CLIENT_ANDROID_PATH)/libs/any.aar
	@hash=$$(shasum -b dist/android/any.aar | cut -d' ' -f1)-any; \
	echo "any.aar version: $$hash"; \
	if [ "$$(uname)" = "Darwin" ]; then \
		sed -i '' "s/anyRuntimeVersion = \".*\"/anyRuntimeVersion = \"$$hash\"/" \
			$(CLIENT_ANDROID_PATH)/gradle/libs.versions.toml; \
		sed -i '' "s|version = '[^']*-any'|version = '$$hash'|" \
			$(CLIENT_ANDROID_PATH)/libs/build.gradle; \
	else \
		sed -i "s/anyRuntimeVersion = \".*\"/anyRuntimeVersion = \"$$hash\"/" \
			$(CLIENT_ANDROID_PATH)/gradle/libs.versions.toml; \
		sed -i "s|version = '[^']*-any'|version = '$$hash'|" \
			$(CLIENT_ANDROID_PATH)/libs/build.gradle; \
	fi
	@echo "Installed any.aar into $(CLIENT_ANDROID_PATH)/libs/"
	@echo "Next: cd $(CLIENT_ANDROID_PATH) && ./gradlew :libs:publishToMavenLocal"

# Opt-in: point go.mod at the local any-sync-sdk checkout for co-development.
# Reversible via `make replace-sdk-remote`. Not part of the default build.
ANY_SYNC_SDK_LOCAL ?= /Users/konstantiniiv/Anytype/any-sync-sdk
.PHONY: replace-sdk-local replace-sdk-remote
replace-sdk-local:
	go mod edit -replace=github.com/anyproto/any-sync-sdk=$(ANY_SYNC_SDK_LOCAL)
	go mod tidy
	@echo "go.mod now points any-sync-sdk at $(ANY_SYNC_SDK_LOCAL)"

replace-sdk-remote:
	go mod edit -dropreplace=github.com/anyproto/any-sync-sdk
	go mod tidy
	@echo "go.mod any-sync-sdk replace removed"
