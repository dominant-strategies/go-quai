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

PRIME_PORT_HTTP=8546
PRIME_PORT_WS=8547
REGION_1_PORT_HTTP=8578
REGION_1_PORT_WS=8579
REGION_2_PORT_HTTP=8580
REGION_2_PORT_WS=8581
REGION_3_PORT_HTTP=8582
REGION_3_PORT_WS=8583
ZONE_1_1_PORT_HTTP=8610
ZONE_1_1_PORT_WS=8611
ZONE_1_2_PORT_HTTP=8642
ZONE_1_2_PORT_WS=8643
ZONE_1_3_PORT_HTTP=8674
ZONE_1_3_PORT_WS=8675
ZONE_2_1_PORT_HTTP=8512
ZONE_2_1_PORT_WS=8613
ZONE_2_2_PORT_HTTP=8544
ZONE_2_2_PORT_WS=8645
ZONE_2_3_PORT_HTTP=8576
ZONE_2_3_PORT_WS=8677
ZONE_3_1_PORT_HTTP=8614
ZONE_3_1_PORT_WS=8615
ZONE_3_2_PORT_HTTP=8646
ZONE_3_2_PORT_WS=8647
ZONE_3_3_PORT_HTTP=8678
ZONE_3_3_PORT_WS=8679

run-full-node:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30303 --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) --quaistats ${NAME}:prime${PASSWORD}@${STATS_HOST}:9000 >> nodelogs/prime.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30304 --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 --quaistats ${NAME}:region1${PASSWORD}@${STATS_HOST}:9100 >> nodelogs/region-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30305 --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 --quaistats ${NAME}:region2${PASSWORD}@${STATS_HOST}:9200 >> nodelogs/region-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30306 --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 --quaistats ${NAME}:region3${PASSWORD}@${STATS_HOST}:9300 >> nodelogs/region-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30307 --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 --quaistats ${NAME}:zone11${PASSWORD}@${STATS_HOST}:9101 >> nodelogs/zone-1-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30308 --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 --quaistats ${NAME}:zone12${PASSWORD}@${STATS_HOST}:9102 >> nodelogs/zone-1-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30309 --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 --quaistats ${NAME}:zone13${PASSWORD}@${STATS_HOST}:9103 >> nodelogs/zone-1-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30310 --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 --quaistats ${NAME}:zone21${PASSWORD}@${STATS_HOST}:9201 >> nodelogs/zone-2-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30311 --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 --quaistats ${NAME}:zone22${PASSWORD}@${STATS_HOST}:9202 >> nodelogs/zone-2-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30312 --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 --quaistats ${NAME}:zone23${PASSWORD}@${STATS_HOST}:9203 >> nodelogs/zone-2-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30313 --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 --quaistats ${NAME}:zone31${PASSWORD}@${STATS_HOST}:9301 >> nodelogs/zone-3-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30314 --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 --quaistats ${NAME}:zone32${PASSWORD}@${STATS_HOST}:9302 >> nodelogs/zone-3-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30315 --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 --quaistats ${NAME}:zone33${PASSWORD}@${STATS_HOST}:9303 >> nodelogs/zone-3-3.log 2>&1 &

PRIME_COINBASE=0x00114a47a5d39ea2022dd4d864cb62cfd16879fc
REGION_1_COINBASE=0x0d79b69c082e6f6a2e78a10a9a49baedb7db37a5
REGION_2_COINBASE=0x3bcec1847c55246cf9ea32a5dfe652f147ac091c
REGION_3_COINBASE=0x5e6b0261c32b25f187786612d27a39f6d0c31771
ZONE_1_1_COINBASE=0x1930e0b28d3766e895df661de871a9b8ab70a4da
ZONE_1_2_COINBASE=0x186da447ec1dd29cdec8cca5653ccc4fd8f9e5e3
ZONE_1_3_COINBASE=0x2ab56840530b1c395ecf91e5923446fa696c7933
ZONE_2_1_COINBASE=0x454f47e9da39a4d2cff17d7ca50757576a298fb2
ZONE_2_2_COINBASE=0x4c190ab6136e94b3b510172784a4fed22f566622
ZONE_2_3_COINBASE=0x5446d13d4907630425928fceb67ca35a6bf1bb0e
ZONE_3_1_COINBASE=0x677c5623aabeb5d6d978cc2ec11ac5297a8afcbd
ZONE_3_2_COINBASE=0x7717ddddd08eacc0bb981c47348d1ec3a99566f8
ZONE_3_3_COINBASE=0x8169c0a78e20ee6e5c53cc18ee2f4eb3f762ee05

run-full-mining:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(PRIME_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30303 --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) --quaistats ${NAME}:prime${PASSWORD}@${STATS_HOST}:9000 >> nodelogs/prime.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(REGION_1_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30304 --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 --quaistats ${NAME}:region1${PASSWORD}@${STATS_HOST}:9100 >> nodelogs/region-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(REGION_2_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30305 --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 --quaistats ${NAME}:region2${PASSWORD}@${STATS_HOST}:9200 >> nodelogs/region-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(REGION_3_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30306 --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 --quaistats ${NAME}:region3${PASSWORD}@${STATS_HOST}:9300 >> nodelogs/region-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_1_1_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30307 --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 --quaistats ${NAME}:zone11${PASSWORD}@${STATS_HOST}:9101 >> nodelogs/zone-1-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_1_2_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30308 --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 --quaistats ${NAME}:zone12${PASSWORD}@${STATS_HOST}:9102 >> nodelogs/zone-1-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_1_3_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30309 --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 --quaistats ${NAME}:zone13${PASSWORD}@${STATS_HOST}:9103 >> nodelogs/zone-1-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_2_1_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30310 --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 --quaistats ${NAME}:zone21${PASSWORD}@${STATS_HOST}:9201 >> nodelogs/zone-2-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_2_2_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30311 --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 --quaistats ${NAME}:zone22${PASSWORD}@${STATS_HOST}:9202 >> nodelogs/zone-2-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_2_3_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30312 --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 --quaistats ${NAME}:zone23${PASSWORD}@${STATS_HOST}:9203 >> nodelogs/zone-2-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_3_1_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30313 --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 --quaistats ${NAME}:zone31${PASSWORD}@${STATS_HOST}:9301 >> nodelogs/zone-3-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_3_2_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30314 --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 --quaistats ${NAME}:zone32${PASSWORD}@${STATS_HOST}:9302 >> nodelogs/zone-3-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --http --ws --mine --miner.threads=4 --miner.etherbase $(ZONE_3_3_COINBASE) --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --port 30315 --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 --quaistats ${NAME}:zone33${PASSWORD}@${STATS_HOST}:9303 >> nodelogs/zone-3-3.log 2>&1 &


run-bootnode:
ifeq (,$(wildcard ./bootnode.key))
	./build/bin/bootnode --genkey=bootnode.key
endif
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30303 --nodekey bootnode.key --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) --quaistats ${NAME}:prime${PASSWORD}@${STATS_HOST}:9000 --nat extip:$(HOST_IP) >> nodelogs/prime.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30304 --nodekey bootnode.key --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 --quaistats ${NAME}:region1${PASSWORD}@${STATS_HOST}:9100 --nat extip:$(HOST_IP) >> nodelogs/region-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30305 --nodekey bootnode.key --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 --quaistats ${NAME}:region2${PASSWORD}@${STATS_HOST}:9200 --nat extip:$(HOST_IP) >> nodelogs/region-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30306 --nodekey bootnode.key --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 --quaistats ${NAME}:region3${PASSWORD}@${STATS_HOST}:9300 --nat extip:$(HOST_IP) >> nodelogs/region-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30307 --nodekey bootnode.key --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 --quaistats ${NAME}:zone11${PASSWORD}@${STATS_HOST}:9101 --nat extip:$(HOST_IP) >> nodelogs/zone-1-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30308 --nodekey bootnode.key --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 --quaistats ${NAME}:zone12${PASSWORD}@${STATS_HOST}:9102 --nat extip:$(HOST_IP) >> nodelogs/zone-1-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30309 --nodekey bootnode.key --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 --quaistats ${NAME}:zone13${PASSWORD}@${STATS_HOST}:9103 --nat extip:$(HOST_IP) >> nodelogs/zone-1-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30310 --nodekey bootnode.key --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 --quaistats ${NAME}:zone21${PASSWORD}@${STATS_HOST}:9201 --nat extip:$(HOST_IP) >> nodelogs/zone-2-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30311 --nodekey bootnode.key --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 --quaistats ${NAME}:zone22${PASSWORD}@${STATS_HOST}:9202 --nat extip:$(HOST_IP) >> nodelogs/zone-2-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30312 --nodekey bootnode.key --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 --quaistats ${NAME}:zone23${PASSWORD}@${STATS_HOST}:9203 --nat extip:$(HOST_IP) >> nodelogs/zone-2-3.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30313 --nodekey bootnode.key --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 --quaistats ${NAME}:zone31${PASSWORD}@${STATS_HOST}:9301 --nat extip:$(HOST_IP) >> nodelogs/zone-3-1.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30314 --nodekey bootnode.key --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 --quaistats ${NAME}:zone32${PASSWORD}@${STATS_HOST}:9302 --nat extip:$(HOST_IP) >> nodelogs/zone-3-2.log 2>&1 &
	@nohup ./build/bin/quai --mainnet --ws --http --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3,quai --ws.origins "*" --http.corsdomain "*" --port 30315 --nodekey bootnode.key --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 --quaistats ${NAME}:zone33${PASSWORD}@${STATS_HOST}:9303 --nat extip:$(HOST_IP) >> nodelogs/zone-3-3.log 2>&1 &

stop:
	@if pgrep quai; then pkill -f ./build/bin/quai; fi
	@echo "Stopping all instances of go-quai"
