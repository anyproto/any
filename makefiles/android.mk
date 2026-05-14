GOMOBILE := $(shell go env GOPATH)/bin/gomobile
GOBIND   := $(shell go env GOPATH)/bin/gobind
ANY_TAGS := gomobile

# Install gomobile + gobind into GOPATH/bin and initialize gomobile.
# Idempotent: safe to re-run.
.PHONY: setup-gomobile
setup-gomobile:
	go install golang.org/x/mobile/cmd/gomobile@latest
	go install golang.org/x/mobile/cmd/gobind@latest
	PATH="$(shell go env GOPATH)/bin:$$PATH" $(GOMOBILE) init

# Produce dist/android/any.aar from the github.com/anyproto/any/mobile
# package. Output AAR contains JNI .so files for all gomobile-supported
# Android ABIs; the consuming APK's ndk.abiFilters picks the subset.
.PHONY: build-android
build-android: setup-gomobile
	@mkdir -p dist/android
	@echo "Building any.aar via gomobile bind..."
	PATH="$(shell go env GOPATH)/bin:$$PATH" $(GOMOBILE) bind \
		-tags "$(ANY_TAGS)" \
		-target=android/arm64 \
		-androidapi 26 \
		-javapkg=io.anyproto.any \
		-o dist/android/any.aar \
		github.com/anyproto/any/mobile
	@echo "Built dist/android/any.aar"

# Drop the freshly-built AAR into a sibling anytype-kotlin2 checkout and
# bump the SHA-keyed version in its Gradle config so the change is picked
# up on the next `gradle :libs:publishToMavenLocal`.
#
# Required: CLIENT_ANDROID_PATH=/path/to/anytype-kotlin2-or-bin-to-apk
.PHONY: install-dev-android
install-dev-android: build-android
ifndef CLIENT_ANDROID_PATH
	$(error CLIENT_ANDROID_PATH must point to the anytype-kotlin2 checkout)
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
