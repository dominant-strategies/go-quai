# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: go-quai all clean

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

go-quai:
	$(GORUN) build/ci.go install ./cmd/go-quai
	@echo "Done building."
	@echo "Run \"$(GOBIN)/go-quai\" to launch go-quai."

bootnode:
	$(GORUN) build/ci.go install ./cmd/bootnode
	@echo "Done building."
	@echo "Run \"$(GOBIN)/bootnode\" to launch bootnode binary."

debug:
	go build -gcflags=all="-N -l" -v -o build/bin/go-quai ./cmd/go-quai

all:
	$(GORUN) build/ci.go install

test: all
	$(GORUN) build/ci.go test

lint: ## Run linters.
	$(GORUN) build/ci.go lint

clean:
	env GO111MODULE=on go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

include network.env

# Build the base command
# WARNING: WS_ADDR is a sensitive interface and should only be exposed to trusted networks
BASE_CMD = ./build/bin/go-quai --$(NETWORK) --syncmode $(SYNCMODE) --verbosity $(VERBOSITY) --nonce $(NONCE)
BASE_CMD += --http --http.vhosts=* --http.addr $(HTTP_ADDR) --http.api $(HTTP_API)
BASE_CMD += --ws --ws.addr $(WS_ADDR) --ws.api $(WS_API)
BASE_CMD += --slices $(SLICES)
BASE_CMD += --db.engine $(DB_ENGINE)
BASE_CMD += $(if $(MAX_PEERS),--maxpeers $(MAX_PEERS))
ifeq ($(ENABLE_NAT),true)
	BASE_CMD += --nat extip:$(EXT_IP)
endif
ifeq ($(ENABLE_UNLOCK),true)
	BASE_CMD += --allow-insecure-unlock
endif
ifeq ($(BOOTNODE),true)
ifndef EXT_IP
$(error Please set EXT_IP variable to your external ip in network.env and rerun the makefile)
endif
	BASE_CMD += --nodekey bootnode.key --ws.origins=$(WS_ORIG) --http.corsdomain=$(HTTP_CORSDOMAIN) --nat extip:$(EXT_IP)
endif
ifeq ($(CORS),true)
	BASE_CMD += --ws.origins=$(WS_ORIG) --http.corsdomain=$(HTTP_CORSDOMAIN)
endif
ifeq ($(QUAI_STATS),true)
	BASE_CMD += --quaistats ${STATS_NAME}:${STATS_PASS}@${STATS_HOST}
endif
ifeq ($(SEND_FULL_STATS),true)
	BASE_CMD += --sendfullstats
endif

ifeq ($(SHOW_COLORS),true)
	BASE_CMD += --showcolors
endif

ifeq ($(RUN_BLAKE3),true)
	BASE_CMD += --consensus.engine "blake3"
endif
ifeq ($(NO_DISCOVER),true)
	BASE_CMD += --nodiscover
endif

ifeq ($(CUSTOM_DATA_DIR), true)
	BASE_CMD += --datadir $(DATA_DIR)
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

run:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@echo "Initializing run..."
	@nohup $(BASE_CMD) --port $(PRIME_PORT_TCP) --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) --sub.urls $(PRIME_SUB_URLS) >> nodelogs/prime.log 2>&1 & echo "Prime Node running with PID: $$!" &
	@nohup $(BASE_CMD) --port $(REGION_0_PORT_TCP) --http.port $(REGION_0_PORT_HTTP) --ws.port $(REGION_0_PORT_WS) --dom.url $(REGION_0_DOM_URL):$(PRIME_PORT_WS) --sub.urls $(REGION_0_SUB_URLS) --region 0 >> nodelogs/region-0.log 2>&1 & echo "Region 0 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --port $(REGION_1_PORT_TCP) --http.port $(REGION_1_PORT_HTTP) --ws.port $(REGION_1_PORT_WS) --dom.url $(REGION_1_DOM_URL):$(PRIME_PORT_WS) --sub.urls $(REGION_1_SUB_URLS) --region 1 >> nodelogs/region-1.log 2>&1 & echo "Region 1 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --port $(REGION_2_PORT_TCP) --http.port $(REGION_2_PORT_HTTP) --ws.port $(REGION_2_PORT_WS) --dom.url $(REGION_2_DOM_URL):$(PRIME_PORT_WS) --sub.urls $(REGION_2_SUB_URLS) --region 2 >> nodelogs/region-2.log 2>&1 & echo "Region 2 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_0_0_COINBASE) --port $(ZONE_0_0_PORT_TCP) --http.port $(ZONE_0_0_PORT_HTTP) --ws.port $(ZONE_0_0_PORT_WS) --dom.url $(ZONE_0_0_DOM_URL):$(REGION_0_PORT_WS) --region 0 --zone 0 >> nodelogs/zone-0-0.log 2>&1 & echo "Zone 0-0 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_0_1_COINBASE) --port $(ZONE_0_1_PORT_TCP) --http.port $(ZONE_0_1_PORT_HTTP) --ws.port $(ZONE_0_1_PORT_WS) --dom.url $(ZONE_0_1_DOM_URL):$(REGION_0_PORT_WS) --region 0 --zone 1 >> nodelogs/zone-0-1.log 2>&1 & echo "Zone 0-1 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_0_2_COINBASE) --port $(ZONE_0_2_PORT_TCP) --http.port $(ZONE_0_2_PORT_HTTP) --ws.port $(ZONE_0_2_PORT_WS) --dom.url $(ZONE_0_2_DOM_URL):$(REGION_0_PORT_WS) --region 0 --zone 2 >> nodelogs/zone-0-2.log 2>&1 & echo "Zone 0-2 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_1_0_COINBASE) --port $(ZONE_1_0_PORT_TCP) --http.port $(ZONE_1_0_PORT_HTTP) --ws.port $(ZONE_1_0_PORT_WS) --dom.url $(ZONE_1_0_DOM_URL):$(REGION_1_PORT_WS) --region 1 --zone 0 >> nodelogs/zone-1-0.log 2>&1 & echo "Zone 1-0 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_1_1_COINBASE) --port $(ZONE_1_1_PORT_TCP) --http.port $(ZONE_1_1_PORT_HTTP) --ws.port $(ZONE_1_1_PORT_WS) --dom.url $(ZONE_1_1_DOM_URL):$(REGION_1_PORT_WS) --region 1 --zone 1 >> nodelogs/zone-1-1.log 2>&1 & echo "Zone 1-1 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_1_2_COINBASE) --port $(ZONE_1_2_PORT_TCP) --http.port $(ZONE_1_2_PORT_HTTP) --ws.port $(ZONE_1_2_PORT_WS) --dom.url $(ZONE_1_2_DOM_URL):$(REGION_1_PORT_WS) --region 1 --zone 2 >> nodelogs/zone-1-2.log 2>&1 & echo "Zone 1-2 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_2_0_COINBASE) --port $(ZONE_2_0_PORT_TCP) --http.port $(ZONE_2_0_PORT_HTTP) --ws.port $(ZONE_2_0_PORT_WS) --dom.url $(ZONE_2_0_DOM_URL):$(REGION_2_PORT_WS) --region 2 --zone 0 >> nodelogs/zone-2-0.log 2>&1 & echo "Zone 2-0 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_2_1_COINBASE) --port $(ZONE_2_1_PORT_TCP) --http.port $(ZONE_2_1_PORT_HTTP) --ws.port $(ZONE_2_1_PORT_WS) --dom.url $(ZONE_2_1_DOM_URL):$(REGION_2_PORT_WS) --region 2 --zone 1 >> nodelogs/zone-2-1.log 2>&1 & echo "Zone 2-1 Node running with PID: $$!" &
	@nohup $(BASE_CMD) --miner.etherbase $(ZONE_2_2_COINBASE) --port $(ZONE_2_2_PORT_TCP) --http.port $(ZONE_2_2_PORT_HTTP) --ws.port $(ZONE_2_2_PORT_WS) --dom.url $(ZONE_2_2_DOM_URL):$(REGION_2_PORT_WS) --region 2 --zone 2 >> nodelogs/zone-2-2.log 2>&1 & echo "Zone 2-2 Node running with PID: $$!" &

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
