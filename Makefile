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
	@echo "Run \"$(GOBIN)/quai\" to launch go-quai."

bootnode:
	$(GORUN) build/ci.go install ./cmd/bootnode
	@echo "Done building."
	@echo "Run \"$(GOBIN)/bootnode\" to launch bootnode binary."

debug:
	go build -gcflags=all="-N -l" -v -o build/bin/go-quai ./cmd/go-quai

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
	@echo "Import \"$(GOBIN)/Quai.framework\" to use the library."

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

go-quai-cross: go-quai-linux go-quai-darwin go-quai-windows go-quai-android go-quai-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-*

go-quai-linux: go-quai-linux-386 go-quai-linux-amd64 go-quai-linux-arm go-quai-linux-mips64 go-quai-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-*

go-quai-linux-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/quai
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep 386

go-quai-linux-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/quai
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep amd64

go-quai-linux-arm: go-quai-linux-arm-5 go-quai-linux-arm-6 go-quai-linux-arm-7 go-quai-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm

go-quai-linux-arm-5:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/quai
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm-5

go-quai-linux-arm-6:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/quai
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm-6

go-quai-linux-arm-7:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/quai
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm-7

go-quai-linux-arm64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/quai
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep arm64

go-quai-linux-mips:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mips

go-quai-linux-mipsle:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mipsle

go-quai-linux-mips64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mips64

go-quai-linux-mips64le:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/quai
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-linux-* | grep mips64le

go-quai-darwin: go-quai-darwin-386 go-quai-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-darwin-*

go-quai-darwin-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/quai
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-darwin-* | grep 386

go-quai-darwin-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/quai
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-darwin-* | grep amd64

go-quai-windows: go-quai-windows-386 go-quai-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-windows-*

go-quai-windows-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/quai
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-windows-* | grep 386

go-quai-windows-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/quai
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/go-quai-windows-* | grep amd64

include network.env

# Build the base command
# WARNING: WS_ADDR is a sensitive interface and should only be exposed to trusted networks
BASE_CMD = ./build/bin/go-quai --$(NETWORK) --syncmode full --verbosity 4
BASE_CMD += --http --http.vhosts=* --http.addr $(HTTP_ADDR) --http.api $(HTTP_API)
BASE_CMD += --ws --ws.addr $(WS_ADDR) --ws.api $(WS_API)
ifeq ($(ENABLE_ARCHIVE),true)
	BASE_CMD += --gcmode archive
endif
ifeq ($(ENABLE_UNLOCK),true)
	BASE_CMD += --allow-insecure-unlock
endif
ifeq ($(BOOTNODE),true)
	BASE_CMD += --nodekey bootnode.key --ws.origins=$(WS_ORIG) --http.corsdomain=$(HTTP_CORSDOMAIN)
endif
ifeq ($(CORS),true)
	BASE_CMD += --ws.origins=$(WS_ORIG) --http.corsdomain=$(HTTP_CORSDOMAIN)
endif
ifeq ($(QUAI_MINING),true)
	BASE_CMD += --mine --miner.threads $(THREADS)
endif

# Build suburl strings for slice specific subclient groups
# WARNING: Only connect to dom/sub clients over a trusted network.
ifeq ($(REGION),2)
	PRIME_SUBS += ,,ws://127.0.0.1:$(REGION_$(REGION)_PORT_WS)
endif
ifeq ($(REGION),1)
	PRIME_SUBS += ,ws://127.0.0.1:$(REGION_$(REGION)_PORT_WS),
endif
ifeq ($(REGION),0)
	PRIME_SUBS += ws://127.0.0.1:$(REGION_$(REGION)_PORT_WS),,
endif
ifeq ($(ZONE),2)
	REGION_SUBS =,,ws://127.0.0.1:$(ZONE_$(REGION)_$(ZONE)_PORT_WS)
endif
ifeq ($(ZONE),1)
	REGION_SUBS =,ws://127.0.0.1:$(ZONE_$(REGION)_$(ZONE)_PORT_WS),
endif
ifeq ($(ZONE),0)
	REGION_SUBS =ws://127.0.0.1:$(ZONE_$(REGION)_$(ZONE)_PORT_WS),,
endif

# Build specific prime, region, and zone commands for run-slice
PRIME_CMD = $(BASE_CMD) --miner.etherbase $(PRIME_COINBASE) --port $(PRIME_PORT_TCP)
PRIME_CMD += --http.port $(PRIME_PORT_HTTP)
PRIME_CMD += --ws.port $(PRIME_PORT_WS)
PRIME_CMD += --sub.urls "$(PRIME_SUBS)"
PRIME_LOG_FILE = nodelogs/prime.log
REGION_CMD = $(BASE_CMD) --region $(REGION) --miner.etherbase $(REGION_$(REGION)_COINBASE) --port $(REGION_$(REGION)_PORT_TCP)
REGION_CMD += --http.port $(REGION_$(REGION)_PORT_HTTP)
REGION_CMD += --ws.port $(REGION_$(REGION)_PORT_WS)
REGION_CMD += --dom.url ws://127.0.0.1:$(PRIME_PORT_WS)
REGION_CMD += --sub.urls "$(REGION_SUBS)"
REGION_LOG_FILE = nodelogs/region-$(REGION).log
ZONE_CMD = $(BASE_CMD) --region $(REGION) --zone $(ZONE) --miner.etherbase $(ZONE_$(REGION)_$(ZONE)_COINBASE) --port $(ZONE_$(REGION)_$(ZONE)_PORT_TCP)
ZONE_CMD += --http.port $(ZONE_$(REGION)_$(ZONE)_PORT_HTTP)
ZONE_CMD += --ws.port $(ZONE_$(REGION)_$(ZONE)_PORT_WS)
ZONE_CMD += --dom.url ws://127.0.0.1:$(REGION_$(REGION)_PORT_WS)
ZONE_LOG_FILE = nodelogs/zone-$(REGION)-$(ZONE).log

run-slice:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	$(PRIME_CMD) >> $(PRIME_LOG_FILE) 2>&1 &
	$(REGION_CMD) >> $(REGION_LOG_FILE) 2>&1 &
	$(ZONE_CMD) >> $(ZONE_LOG_FILE) 2>&1 &

run-all:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup $(BASE_CMD) --miner.etherbase $(PRIME_COINBASE)    --port $(PRIME_PORT_TCP)    --http.port $(PRIME_PORT_HTTP)    --ws.port $(PRIME_PORT_WS)                                                      --sub.urls $(PRIME_SUB_URLS)                        >> nodelogs/prime.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(REGION_0_COINBASE) --port $(REGION_0_PORT_TCP) --http.port $(REGION_0_PORT_HTTP) --ws.port $(REGION_0_PORT_WS) --dom.url $(REGION_0_DOM_URL):$(PRIME_PORT_WS)    --sub.urls $(REGION_0_SUB_URLS) --region 0          >> nodelogs/region-0.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(REGION_1_COINBASE) --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --dom.url $(REGION_1_DOM_URL):$(PRIME_PORT_WS)    --sub.urls $(REGION_1_SUB_URLS) --region 1          >> nodelogs/region-1.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(REGION_2_COINBASE) --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --dom.url $(REGION_2_DOM_URL):$(PRIME_PORT_WS)    --sub.urls $(REGION_2_SUB_URLS) --region 2          >> nodelogs/region-2.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_0_0_COINBASE) --port $(ZONE_0_0_PORT_TCP) --http.port $(ZONE_0_0_PORT_HTTP) --ws.port $(ZONE_0_0_PORT_WS) --dom.url $(ZONE_0_0_DOM_URL):$(REGION_0_PORT_WS)                                 --region 0 --zone 0 >> nodelogs/zone-0-0.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_0_1_COINBASE) --port $(ZONE_0_1_PORT_TCP) --http.port $(ZONE_0_1_PORT_HTTP) --ws.port $(ZONE_0_1_PORT_WS) --dom.url $(ZONE_0_1_DOM_URL):$(REGION_0_PORT_WS)                                 --region 0 --zone 1 >> nodelogs/zone-0-1.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_0_2_COINBASE) --port $(ZONE_0_2_PORT_TCP) --http.port $(ZONE_0_2_PORT_HTTP) --ws.port $(ZONE_0_2_PORT_WS) --dom.url $(ZONE_0_2_DOM_URL):$(REGION_0_PORT_WS)                                 --region 0 --zone 2 >> nodelogs/zone-0-2.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_1_0_COINBASE) --port $(ZONE_1_0_PORT_TCP) --http.port $(ZONE_1_0_PORT_HTTP) --ws.port $(ZONE_1_0_PORT_WS) --dom.url $(ZONE_1_0_DOM_URL):$(REGION_1_PORT_WS)                                 --region 1 --zone 0 >> nodelogs/zone-1-0.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_1_1_COINBASE) --port $(ZONE_1_1_PORT_TCP) --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --dom.url $(ZONE_1_1_DOM_URL):$(REGION_1_PORT_WS)                                 --region 1 --zone 1 >> nodelogs/zone-1-1.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_1_2_COINBASE) --port $(ZONE_1_2_PORT_TCP) --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --dom.url $(ZONE_1_2_DOM_URL):$(REGION_1_PORT_WS)                                 --region 1 --zone 2 >> nodelogs/zone-1-2.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_2_0_COINBASE) --port $(ZONE_2_0_PORT_TCP) --http.port $(ZONE_2_0_PORT_HTTP) --ws.port $(ZONE_2_0_PORT_WS) --dom.url $(ZONE_2_0_DOM_URL):$(REGION_2_PORT_WS)                                 --region 2 --zone 0 >> nodelogs/zone-2-0.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_2_1_COINBASE) --port $(ZONE_2_1_PORT_TCP) --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --dom.url $(ZONE_2_1_DOM_URL):$(REGION_2_PORT_WS)                                 --region 2 --zone 1 >> nodelogs/zone-2-1.log 2>&1 &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_2_2_COINBASE) --port $(ZONE_2_2_PORT_TCP) --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --dom.url $(ZONE_2_2_DOM_URL):$(REGION_2_PORT_WS)                                 --region 2 --zone 2 >> nodelogs/zone-2-2.log 2>&1 &

stop:
ifeq ($(shell uname -s), $(filter $(shell uname -s), Darwin Linux))
	@-pkill -f ./build/bin/go-quai;
	@while pgrep quai >/dev/null; do \
		echo "Stopping all Quai Network nodes, please wait until terminated."; \
		sleep 3; \
	done;
else
	@echo "Stopping all Quai Network nodes, please wait until terminated.";
	@if pgrep quai; then killall -w quai; fi
endif
