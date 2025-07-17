include config.mk

BIN=ai-mcpd
MAIN_FILE=main.go

.PHONY: all run clean deps

all:
	$(MAKE) -C ai-mcpd
	$(MAKE) -C servers/wttr
	$(MAKE) -C clients/ai-repl
	$(MAKE) -C clients/ai-tools
	./ai-mcpd/ai-mcpd 'r2pm -r r2mcp' servers/wttr/wttr

install:
	$(MAKE) -C ai-mcpd install
	$(MAKE) -C clients/ai-repl install
	$(MAKE) -C clients/ai-tools install


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
