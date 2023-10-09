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
ifeq ($(ENABLE_ARCHIVE),true)
	BASE_CMD += --gcmode archive
endif
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

ifeq ($(SHOW_COLORS),true)
	BASE_CMD += --showcolors
endif

ifeq ($(RUN_BLAKE3),true)
	BASE_CMD += --consensus.engine "blake3"
endif
ifeq ($(NO_DISCOVER),true)
	BASE_CMD += --nodiscover
endif

build_sequence = $(if $(filter $1,$2),,$1 $(call build_sequence,$(shell expr $1 + 1),$2))

# Generate the sequence outside of any target
WIDTH_SEQ := $(call build_sequence,0,$(WIDTH))

# Step 2: Execute the commands in the run target's recipe
# Command for the prime node
PRIME_CMD := nohup $(BASE_CMD) --port $(PRIME_PORT_TCP) --http.port $(PRIME_PORT_HTTP) --ws.port $(PRIME_PORT_WS) --sub.urls $(PRIME_SUB_URLS) >> nodelogs/prime.log 2>&1 &

# Template for the region nodes
define REGION_CMD
nohup $(BASE_CMD) --port $(REGION_$(1)_PORT_TCP) --http.port $(REGION_$(1)_PORT_HTTP) --ws.port $(REGION_$(1)_PORT_WS) --dom.url $(REGION_$(1)_DOM_URL):$(PRIME_PORT_WS) --sub.urls $(REGION_$(1)_SUB_URLS) --region $(1) >> nodelogs/region-$(1).log 2>&1 &
endef

# Template for the zone nodes
define ZONE_CMD
nohup $(BASE_CMD) --miner.etherbase $(ZONE_$(1)_$(2)_COINBASE) --port $(ZONE_$(1)_$(2)_PORT_TCP) --http.port $(ZONE_$(1)_$(2)_PORT_HTTP) --ws.port $(ZONE_$(1)_$(2)_PORT_WS) --dom.url $(ZONE_$(1)_$(2)_DOM_URL):$(REGION_$(1)_PORT_WS) --region $(1) --zone $(2) >> nodelogs/zone-$(1)-$(2).log 2>&1 &
endef

# Run all the commands within the run target
run:
ifeq (,$(wildcard nodelogs))
	mkdir nodelogs
endif
	@$(PRIME_CMD)
	@$(foreach r,$(WIDTH_SEQ),$(call REGION_CMD,$(r)))
	@$(foreach r,$(WIDTH_SEQ),$(foreach z,$(WIDTH_SEQ),$(call ZONE_CMD,$(r),$(z))))

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
