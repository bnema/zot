.PHONY: default build install uninstall

BIN := $(notdir $(CURDIR))
# Must match "name" in extension.json (slash command/logical extension name).
EXT := mcp
ZUT_HOME ?= $(shell \
	if [ -n "$$XDG_STATE_HOME" ]; then \
		printf '%s/zut' "$$XDG_STATE_HOME"; \
	elif [ "$$(uname -s)" = "Darwin" ]; then \
		printf '%s/Library/Application Support/zut' "$$HOME"; \
	else \
		printf '%s/.local/state/zut' "$$HOME"; \
	fi)

default: install

build:
	go build -o $(BIN) .

install:
	-zut ext remove $(EXT) --yes
	rm -rf "$(ZUT_HOME)/extensions/$(BIN)"
	mkdir -p "$(ZUT_HOME)/extensions/$(EXT)"
	cp extension.json "$(ZUT_HOME)/extensions/$(EXT)/extension.json"
	go build -o "$(ZUT_HOME)/extensions/$(EXT)/$(BIN)" .

uninstall:
	-zut ext remove $(EXT) --yes
