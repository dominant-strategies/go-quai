# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: go-quai android ios geth-cross evm all test clean
.PHONY: go-quai-linux geth-linux-386 geth-linux-amd64 geth-linux-mips64 geth-linux-mips64le
.PHONY: go-quai-linux-arm geth-linux-arm-5 geth-linux-arm-6 geth-linux-arm-7 geth-linux-arm64
.PHONY: go-quai-darwin geth-darwin-386 geth-darwin-amd64
.PHONY: go-quai-windows geth-windows-386 geth-windows-amd64

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

go-quai:
	$(GORUN) build/ci.go install ./cmd/go-quai
	@echo "Done building."
	@echo "Run \"$(GOBIN)/go-quai\" to launch geth."

all:
	$(GORUN) build/ci.go install

android:
	$(GORUN) build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/go-quai.aar\" to use the library."
	@echo "Import \"$(GOBIN)/go-quai-sources.jar\" to add javadocs"
	@echo "For more info see https://stackoverflow.com/questions/20994336/android-studio-how-to-attach-javadoc"

ios:
	$(GORUN) build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/Geth.framework\" to use the library."

test: all
	$(GORUN) build/ci.go test

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	env GO111MODULE=on go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go install golang.org/x/tools/cmd/stringer@latest
	env GOBIN= go install github.com/kevinburke/go-bindata/go-bindata@latest
	env GOBIN= go install github.com/fjl/gencodec@latest
	env GOBIN= go install github.com/golang/protobuf/protoc-gen-go@latest
	env GOBIN= go install ./cmd/abigen
	@type "solc" 2> /dev/null || echo 'Please install solc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

# Cross Compilation Targets (xgo)

go-quai-cross: geth-linux geth-darwin geth-windows geth-android geth-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-*

go-quai-linux: geth-linux-386 geth-linux-amd64 geth-linux-arm geth-linux-mips64 geth-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-*

go-quai-linux-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/go-quai
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep 386

go-quai-linux-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/go-quai
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep amd64

go-quai-linux-arm: geth-linux-arm-5 geth-linux-arm-6 geth-linux-arm-7 geth-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm

go-quai-linux-arm-5:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/go-quai
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm-5

go-quai-linux-arm-6:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/go-quai
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm-6

go-quai-linux-arm-7:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/go-quai
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm-7

go-quai-linux-arm64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/go-quai
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm64

go-quai-linux-mips:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/go-quai
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mips

go-quai-linux-mipsle:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/go-quai
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mipsle

go-quai-linux-mips64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/go-quai
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mips64

go-quai-linux-mips64le:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/go-quai
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mips64le

go-quai-darwin: geth-darwin-386 geth-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-darwin-*

go-quai-darwin-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/go-quai
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-darwin-* | grep 386

go-quai-darwin-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/go-quai
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-darwin-* | grep amd64

go-quai-windows: geth-windows-386 geth-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-windows-*

go-quai-windows-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/go-quai
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-windows-* | grep 386

go-quai-windows-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/go-quai
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-windows-* | grep amd64
