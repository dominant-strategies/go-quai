# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: geth android ios geth-cross evm all test clean
.PHONY: geth-linux geth-linux-386 geth-linux-amd64 geth-linux-mips64 geth-linux-mips64le
.PHONY: geth-linux-arm geth-linux-arm-5 geth-linux-arm-6 geth-linux-arm-7 geth-linux-arm64
.PHONY: geth-darwin geth-darwin-386 geth-darwin-amd64
.PHONY: geth-windows geth-windows-386 geth-windows-amd64

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

go-quai:
	$(GORUN) build/ci.go install ./cmd/geth
	@echo "Done building."
	@echo "Run \"$(GOBIN)/geth\" to launch go-quai."

bootnode:
	$(GORUN) build/ci.go install ./cmd/bootnode
	@echo "Done building."
	@echo "Run \"$(GOBIN)/bootnode\" to launch bootnode binary."

debug:
	go build -gcflags=all="-N -l" -v -o build/bin/geth ./cmd/geth

all:
	$(GORUN) build/ci.go install

android:
	$(GORUN) build/ci.go aar --local
	@echo "Done building."
	@echo "Import \"$(GOBIN)/geth.aar\" to use the library."
	@echo "Import \"$(GOBIN)/geth-sources.jar\" to add javadocs"
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

geth-cross: geth-linux geth-darwin geth-windows geth-android geth-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/geth-*

geth-linux: geth-linux-386 geth-linux-amd64 geth-linux-arm geth-linux-mips64 geth-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-*

geth-linux-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/geth
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep 386

geth-linux-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/geth
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep amd64

geth-linux-arm: geth-linux-arm-5 geth-linux-arm-6 geth-linux-arm-7 geth-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep arm

geth-linux-arm-5:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/geth
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep arm-5

geth-linux-arm-6:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/geth
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep arm-6

geth-linux-arm-7:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/geth
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep arm-7

geth-linux-arm64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/geth
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep arm64

geth-linux-mips:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep mips

geth-linux-mipsle:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep mipsle

geth-linux-mips64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep mips64

geth-linux-mips64le:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/geth
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/geth-linux-* | grep mips64le

geth-darwin: geth-darwin-386 geth-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/geth-darwin-*

geth-darwin-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/geth
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/geth-darwin-* | grep 386

geth-darwin-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/geth
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/geth-darwin-* | grep amd64

geth-windows: geth-windows-386 geth-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/geth-windows-*

geth-windows-386:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/geth
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/geth-windows-* | grep 386

geth-windows-amd64:
	$(GORUN) build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/geth
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/geth-windows-* | grep amd64

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
ZONE_1_2_PORT_HTTP=8542
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
ifeq (,$(wildcard ./bootnode.key))
	./build/bin/bootnode --genkey=bootnode.key
endif
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30303 --nodekey bootnode.key --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) > nodelogs/prime.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30304 --nodekey bootnode.key --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 > nodelogs/region-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30305 --nodekey bootnode.key --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 > nodelogs/region-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30306 --nodekey bootnode.key --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 > nodelogs/region-3.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30307 --nodekey bootnode.key --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 > nodelogs/zone-1-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30308 --nodekey bootnode.key --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 > nodelogs/zone-1-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30309 --nodekey bootnode.key --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 > nodelogs/zone-1-3.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30310 --nodekey bootnode.key --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 > nodelogs/zone-2-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30311 --nodekey bootnode.key --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 > nodelogs/zone-2-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30312 --nodekey bootnode.key --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 > nodelogs/zone-2-3.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30313 --nodekey bootnode.key --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 > nodelogs/zone-3-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30314 --nodekey bootnode.key --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 > nodelogs/zone-3-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30315 --nodekey bootnode.key --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 > nodelogs/zone-3-3.log 2>&1 &

run-full-mining:
ifeq (,$(wildcard ./bootnode.key))
	./build/bin/bootnode --genkey=bootnode.key
endif
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@nohup ./build/bin/geth --ws --mine --miner.threads=4 --miner.etherbase 0x990eAa88FD08902a4f396beA0285385887Bed5a6 --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30303 --nodekey bootnode.key --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) > nodelogs/prime.log 2>&1 &
	@nohup ./build/bin/geth --ws --mine --miner.threads=4 --miner.etherbase 0x1a6ad97c8f06c7ae79fea47e43a8c048da5b1f7d --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30304 --nodekey bootnode.key --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --region 1 > nodelogs/region-1.log 2>&1 &
	@nohup ./build/bin/geth --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30305 --nodekey bootnode.key --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --region 2 > nodelogs/region-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30306 --nodekey bootnode.key --http.port $(REGION_3_PORT_HTTP) --ws.port $(REGION_3_PORT_WS) --region 3 > nodelogs/region-3.log 2>&1 &
	@nohup ./build/bin/geth --ws  --ws --mine --miner.threads=4 --miner.etherbase 0x1a6ad97c8f06c7ae79fea47e43a8c048da5b1f7d --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30307 --nodekey bootnode.key --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --region 1 --zone 1 > nodelogs/zone-1-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30308 --nodekey bootnode.key --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --region 1 --zone 2 > nodelogs/zone-1-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30309 --nodekey bootnode.key --http.port $(ZONE_1_3_PORT_HTTP) --ws.port $(ZONE_1_3_PORT_WS) --region 1 --zone 3 > nodelogs/zone-1-3.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30310 --nodekey bootnode.key --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --region 2 --zone 1 > nodelogs/zone-2-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30311 --nodekey bootnode.key --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --region 2 --zone 2 > nodelogs/zone-2-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30312 --nodekey bootnode.key --http.port $(ZONE_2_3_PORT_HTTP) --ws.port $(ZONE_2_3_PORT_WS) --region 2 --zone 3 > nodelogs/zone-2-3.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30313 --nodekey bootnode.key --http.port $(ZONE_3_1_PORT_HTTP) --ws.port $(ZONE_3_1_PORT_WS) --region 3 --zone 1 > nodelogs/zone-3-1.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30314 --nodekey bootnode.key --http.port $(ZONE_3_2_PORT_HTTP) --ws.port $(ZONE_3_2_PORT_WS) --region 3 --zone 2 > nodelogs/zone-3-2.log 2>&1 &
	@nohup ./build/bin/geth --ws --syncmode full --allow-insecure-unlock --http.addr 0.0.0.0 --ws.addr 0.0.0.0 --ws.api eth,net,web3 --ws.origins "*" --port 30315 --nodekey bootnode.key --http.port $(ZONE_3_3_PORT_HTTP) --ws.port $(ZONE_3_3_PORT_WS) --region 3 --zone 3 > nodelogs/zone-3-3.log 2>&1 &

stop:
	pkill -f './build/bin/geth'