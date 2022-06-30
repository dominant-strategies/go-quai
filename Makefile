# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: quai android ios quai-cross evm all test clean
.PHONY: quai-linux quai-linux-386 quai-linux-amd64 quai-linux-mips64 quai-linux-mips64le
.PHONY: quai-linux-arm quai-linux-arm-5 quai-linux-arm-6 quai-linux-arm-7 quai-linux-arm64
.PHONY: quai-darwin quai-darwin-386 quai-darwin-amd64
.PHONY: quai-windows quai-windows-386 quai-windows-amd64

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

go-quai:
	$(GORUN) build/ci.go install ./cmd/quai
	@echo "Done building."
	@echo "Run \"$(GOBIN)/quai\" to launch go-quai."

bootnode:
	$(GORUN) build/ci.go install ./cmd/bootnode
	@echo "Done building."
	@echo "Run \"$(GOBIN)/bootnode\" to launch bootnode binary."

debug:
	go build -gcflags=all="-N -l" -v -o build/bin/quai ./cmd/quai

all:
	$(GORUN) build/ci.go install

android:
	$(GORUN) build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/quai.aar\" to use the library."
	@echo "Import \"$(GOBIN)/quai-sources.jar\" to add javadocs"
	@echo "For more info see https://stackoverflow.com/questions/20994336/android-studio-how-to-attach-javadoc"

ios:
	$(GORUN) build/ci.go xcode --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/quai.framework\" to use the library."

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

quai-cross: quai-linux quai-darwin quai-windows quai-android quai-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/quai-*

quai-linux: quai-linux-386 quai-linux-amd64 quai-linux-arm quai-linux-mips64 quai-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-*

quai-linux-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/quai
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep 386

quai-linux-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/quai
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep amd64

quai-linux-arm: quai-linux-arm-5 quai-linux-arm-6 quai-linux-arm-7 quai-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep arm

quai-linux-arm-5:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/quai
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep arm-5

quai-linux-arm-6:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/quai
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep arm-6

quai-linux-arm-7:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/quai
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep arm-7

quai-linux-arm64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/quai
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep arm64

quai-linux-mips:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep mips

quai-linux-mipsle:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep mipsle

quai-linux-mips64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep mips64

quai-linux-mips64le:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/quai-linux-* | grep mips64le

quai-darwin: quai-darwin-386 quai-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/quai-darwin-*

quai-darwin-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/quai
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/quai-darwin-* | grep 386

quai-darwin-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/quai
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/quai-darwin-* | grep amd64

quai-windows: quai-windows-386 quai-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/quai-windows-*

quai-windows-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/quai
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/quai-windows-* | grep 386

quai-windows-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/quai
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/quai-windows-* | grep amd64

include network.env

BASE_COMMAND = ./build/bin/quai --$(NETWORK) --syncmode full 

ifeq ($(ENABLE_ARCHIVE),true)
	BASE_COMMAND += --gcmode archive
endif

ifeq ($(ENABLE_HTTP),true)
	BASE_COMMAND += --http --http.vhosts=* 
endif

ifeq ($(ENABLE_WS),true)
	BASE_COMMAND += --ws
endif

ifeq ($(ENABLE_UNLOCK),true)
	BASE_COMMAND += --allow-insecure-unlock
endif

ifeq ($(QUAI_MINING),true)
	MINING_BASE_COMMAND = $(BASE_COMMAND) --mine --miner.threads $(THREADS)
endif

ifeq ($(BOOTNODE),true)
	BASE_COMMAND += --nodekey bootnode.key --ws.origins $(WS_ORIG) --http.corsdomain $(HTTP_CORSDOMAIN)
endif

ifeq ($(CORS),true)
	BASE_COMMAND += --ws.origins $(WS_ORIG) --http.corsdomain $(HTTP_CORSDOMAIN)
endif

run-slice:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(PRIME_PORT_TCP) --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) >> nodelogs/prime.log 2>&1 &
ifeq ($(SLICE_NUM),1)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 >> nodelogs/region-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_1_PORT_TCP) --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 >> nodelogs/zone-1-1.log 2>&1 &
endif
ifeq ($(SLICE_NUM),2)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 >> nodelogs/region-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_2_PORT_TCP) --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 >> nodelogs/zone-1-2.log 2>&1 &
endif
ifeq ($(SLICE_NUM),3)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 >> nodelogs/region-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_3_PORT_TCP) --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 >> nodelogs/zone-1-3.log 2>&1 &
endif
ifeq ($(SLICE_NUM),4)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 >> nodelogs/region-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_1_PORT_TCP) --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 >> nodelogs/zone-2-1.log 2>&1 &
endif
ifeq ($(SLICE_NUM),5)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 >> nodelogs/region-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_2_PORT_TCP) --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 >> nodelogs/zone-2-2.log 2>&1 &
endif
ifeq ($(SLICE_NUM),6)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 >> nodelogs/region-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_3_PORT_TCP) --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 >> nodelogs/zone-2-3.log 2>&1 &
endif
ifeq ($(SLICE_NUM),7)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_3_PORT_TCP) --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 >> nodelogs/region-3.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_1_PORT_TCP) --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 >> nodelogs/zone-3-1.log 2>&1 &
endif
ifeq ($(SLICE_NUM),8)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_3_PORT_TCP) --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 >> nodelogs/region-3.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_2_PORT_TCP) --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 >> nodelogs/zone-3-2.log 2>&1 &
endif
ifeq ($(SLICE_NUM),9)
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_3_PORT_TCP) --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 >> nodelogs/region-3.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_3_PORT_TCP) --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 >> nodelogs/zone-3-3.log 2>&1 &
endif

run-full-node:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(PRIME_PORT_TCP) --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) >> nodelogs/prime.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 >> nodelogs/region-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 >> nodelogs/region-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_3_PORT_TCP) --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 >> nodelogs/region-3.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_1_PORT_TCP) --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 >> nodelogs/zone-1-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_2_PORT_TCP) --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 >> nodelogs/zone-1-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_3_PORT_TCP) --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 >> nodelogs/zone-1-3.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_1_PORT_TCP) --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 >> nodelogs/zone-2-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_2_PORT_TCP) --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 >> nodelogs/zone-2-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_3_PORT_TCP) --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 >> nodelogs/zone-2-3.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_1_PORT_TCP) --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 >> nodelogs/zone-3-1.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_2_PORT_TCP) --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 >> nodelogs/zone-3-2.log 2>&1 &
	@nohup $(BASE_COMMAND) --http.addr $(HTTP_ADDR) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_3_PORT_TCP) --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 >> nodelogs/zone-3-3.log 2>&1 &

run-full-mining:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(PRIME_COINBASE) --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(PRIME_PORT_TCP) --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) >> nodelogs/prime.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(REGION_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 >> nodelogs/region-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(REGION_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 >> nodelogs/region-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(REGION_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_3_PORT_TCP) --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 >> nodelogs/region-3.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_1_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_1_PORT_TCP) --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 >> nodelogs/zone-1-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_1_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_2_PORT_TCP) --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 >> nodelogs/zone-1-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_1_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_3_PORT_TCP) --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 >> nodelogs/zone-1-3.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_2_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_1_PORT_TCP) --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 >> nodelogs/zone-2-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_2_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_2_PORT_TCP) --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 >> nodelogs/zone-2-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_2_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_3_PORT_TCP) --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 >> nodelogs/zone-2-3.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_3_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_1_PORT_TCP) --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 >> nodelogs/zone-3-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_3_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_2_PORT_TCP) --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 >> nodelogs/zone-3-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_3_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_3_PORT_TCP) --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 >> nodelogs/zone-3-3.log 2>&1 &

run-stats:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(PRIME_COINBASE) --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(PRIME_PORT_TCP) --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) --quaistats ${STATS_NAME}:prime${STATS_PASS}@${PRIME_STATS_HOST} >> nodelogs/prime.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(REGION_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 --quaistats ${STATS_NAME}:region1${STATS_PASS}@${REGION_1_STATS_HOST} >> nodelogs/region-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(REGION_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 --quaistats ${STATS_NAME}:region2${STATS_PASS}@${REGION_2_STATS_HOST} >> nodelogs/region-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(REGION_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(REGION_3_PORT_TCP) --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 --quaistats ${STATS_NAME}:region3${STATS_PASS}@${REGION_3_STATS_HOST} >> nodelogs/region-3.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_1_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_1_PORT_TCP) --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 --quaistats ${STATS_NAME}:zone11${STATS_PASS}@${ZONE_1_1_STATS_HOST} >> nodelogs/zone-1-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_1_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_2_PORT_TCP) --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 --quaistats ${STATS_NAME}:zone12${STATS_PASS}@${ZONE_1_2_STATS_HOST} >> nodelogs/zone-1-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_1_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_1_3_PORT_TCP) --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 --quaistats ${STATS_NAME}:zone13${STATS_PASS}@${ZONE_1_3_STATS_HOST} >> nodelogs/zone-1-3.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_2_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_1_PORT_TCP) --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 --quaistats ${STATS_NAME}:zone21${STATS_PASS}@${ZONE_2_1_STATS_HOST} >> nodelogs/zone-2-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_2_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_2_PORT_TCP) --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 --quaistats ${STATS_NAME}:zone22${STATS_PASS}@${ZONE_2_2_STATS_HOST} >> nodelogs/zone-2-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_2_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_2_3_PORT_TCP) --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 --quaistats ${STATS_NAME}:zone23${STATS_PASS}@${ZONE_2_3_STATS_HOST} >> nodelogs/zone-2-3.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_3_1_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_1_PORT_TCP) --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 --quaistats ${STATS_NAME}:zone31${STATS_PASS}@${ZONE_3_1_STATS_HOST} >> nodelogs/zone-3-1.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_3_2_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_2_PORT_TCP) --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 --quaistats ${STATS_NAME}:zone32${STATS_PASS}@${ZONE_3_2_STATS_HOST} >> nodelogs/zone-3-2.log 2>&1 &
	@nohup $(MINING_BASE_COMMAND) --miner.etherbase $(ZONE_3_3_COINBASE)  --http.addr $(HTTP_ADDR) --http.api $(HTTP_API) --ws.addr $(WS_ADDR) --ws.api $(WS_API)  --port $(ZONE_3_3_PORT_TCP) --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 --quaistats ${STATS_NAME}:zone33${STATS_PASS}@${ZONE_3_3_STATS_HOST} >> nodelogs/zone-3-3.log 2>&1 &

stop:
ifeq ($(shell uname -s),Darwin)
	@if pgrep quai; then pkill -f ./build/bin/quai; fi
	@while pgrep quai >/dev/null; do \
		echo "Stopping all Quai Network nodes, please wait until terminated."; \
		sleep 3; \
	done;
else
	@echo "Stopping all Quai Network nodes, please wait until terminated.";
	@if pgrep quai; then killall -w quai; fi
endif
