include config.mk

BIN=ai-mcpd
MAIN_FILE=main.go

.PHONY: all run clean deps

all:
	$(MAKE) -C src/mcpd
	$(MAKE) -C src/servers/wttr
	$(MAKE) -C src/repl
	$(MAKE) -C src/tool
	./src/mcpd/acli-mcpd 'r2pm -r r2mcp' src/servers/wttr/acli-mcp-wttr

install:
	$(MAKE) -C src/mcpd install
	$(MAKE) -C src/repl install
	$(MAKE) -C src/tool install
	$(MAKE) -C src/servers install

run:
	go run $(MAIN_FILE)

clean:
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

test:
	go test ./...

.DEFAULT_GOAL := all
