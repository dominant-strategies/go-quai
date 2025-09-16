.DEFAULT_GOAL := help
.PHONY: all build run test clean help mocks

GOBIN = ./build/bin
GO ?= latest
GORUN = env GO111MODULE=on go run

## This help screen
help:
	@printf "Available targets:\n\n"
	@awk '/^[a-zA-Z\-\_0-9%:\\]+/ { \
	helpMessage = match(lastLine, /^## (.*)/); \
	if (helpMessage) { \
	helpCommand = $$1; \
	helpMessage = substr(lastLine, RSTART + 3, RLENGTH); \
	gsub("\\\\", "", helpCommand); \
	gsub(":+$$", "", helpCommand); \
	printf "  \x1b[32;01m%-35s\x1b[0m %s\n", helpCommand, helpMessage; \
	} \
        } \
        { lastLine = $$0 }' $(MAKEFILE_LIST) | sort -u
	@printf "\n"

## generate mocks. 
mocks:
	@echo "Generating mocks"
	@ mockgen -package mocks -destination p2p/protocol/mocks/mockedQuaiP2PNode.go -source=p2p/protocol/interface.go QuaiP2PNode
	@ mockgen -package mocks -destination common/mocks/mockedStream.go -source=common/mocks/stream.go Stream

## generate protobuf files
protogen:
	@echo "Generating protobuf files"
	@find . -name '*.proto' -exec protoc --go_out=. --go_opt=paths=source_relative --experimental_allow_proto3_optional {} \;

debug:
	go build -gcflags=all="-N -l" -v -o build/bin/go-quai ./cmd/go-quai

## build the go-quai binary
go-quai:
	$(GORUN) build/ci.go build ./cmd/go-quai
	@echo "Done building."
	@echo "Run \"$(GOBIN)/go-quai\" to launch go-quai."
