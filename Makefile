.DEFAULT_GOAL := help
.PHONY: all build run test clean help mocks


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