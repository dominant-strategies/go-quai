MODULE = github.com/dominant-strategies/go-quai
EXPORT_PKG = $(MODULE)/mobile/export
BUILD_DIR = ../build/mobile

# ---- macOS (native, for testing) ----
macos:
	CGO_ENABLED=1 \
	go build -buildmode=c-archive \
	  -o $(BUILD_DIR)/macos/libquaiwallet.a $(EXPORT_PKG)

# ---- iOS (arm64, c-archive) ----
ios:
	CGO_ENABLED=1 \
	GOOS=ios \
	GOARCH=arm64 \
	SDK=iphoneos \
	CC=$$(xcrun --sdk iphoneos --find clang) \
	CGO_CFLAGS="-isysroot $$(xcrun --sdk iphoneos --show-sdk-path) -arch arm64 -mios-version-min=13.0" \
	CGO_LDFLAGS="-isysroot $$(xcrun --sdk iphoneos --show-sdk-path) -arch arm64" \
	go build -buildmode=c-archive -tags mobile \
	  -o $(BUILD_DIR)/ios/libquaiwallet.a $(EXPORT_PKG)

# ---- iOS Simulator (arm64, c-archive) ----
ios-sim:
	CGO_ENABLED=1 \
	GOOS=ios \
	GOARCH=arm64 \
	CC=$$(xcrun --sdk iphonesimulator --find clang) \
	CGO_CFLAGS="-isysroot $$(xcrun --sdk iphonesimulator --show-sdk-path) -arch arm64 -miphonesimulator-version-min=13.0" \
	CGO_LDFLAGS="-isysroot $$(xcrun --sdk iphonesimulator --show-sdk-path) -arch arm64" \
	go build -buildmode=c-archive -tags mobile \
	  -o $(BUILD_DIR)/ios-sim/libquaiwallet.a $(EXPORT_PKG)

# ---- Android (arm64, c-shared) ----
ANDROID_NDK ?= $(ANDROID_HOME)/ndk/$(shell ls $(ANDROID_HOME)/ndk/ 2>/dev/null | sort -V | tail -1)
ANDROID_API ?= 21

android-arm64:
	CGO_ENABLED=1 \
	GOOS=android \
	GOARCH=arm64 \
	CC=$(ANDROID_NDK)/toolchains/llvm/prebuilt/$$(uname -s | tr A-Z a-z)-x86_64/bin/aarch64-linux-android$(ANDROID_API)-clang \
	go build -buildmode=c-shared \
	  -o $(BUILD_DIR)/android/arm64-v8a/libquaiwallet.so $(EXPORT_PKG)

android-x86_64:
	CGO_ENABLED=1 \
	GOOS=android \
	GOARCH=amd64 \
	CC=$(ANDROID_NDK)/toolchains/llvm/prebuilt/$$(uname -s | tr A-Z a-z)-x86_64/bin/x86_64-linux-android$(ANDROID_API)-clang \
	go build -buildmode=c-shared \
	  -o $(BUILD_DIR)/android/x86_64/libquaiwallet.so $(EXPORT_PKG)

android: android-arm64 android-x86_64

# ---- All platforms ----
all: macos ios ios-sim android

# ---- Tests ----
test:
	go test ./mobile/hdwallet/... -v -count=1

# ---- Clean ----
clean:
	rm -rf $(BUILD_DIR)

.PHONY: macos ios ios-sim android-arm64 android-x86_64 android all test clean
