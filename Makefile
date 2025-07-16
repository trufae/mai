include config.mk

BIN=ai-mcpd
MAIN_FILE=main.go

.PHONY: all build run clean deps

all: build

build:
	go build -o $(BIN) $(MAIN_FILE)

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN) $(DESTDIR)$(PREFIX)/bin/$(BIN)
	$(MAKE) -C clients/ai-repl install
	$(MAKE) -C clients/ai-tools install

full f:
	$(MAKE)
	$(MAKE) -C servers/wttr
	$(MAKE) -C clients/ai-repl
	$(MAKE) -C clients/ai-tools
	./mcpd 'r2pm -r r2mcp' servers/wttr/wttr


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

.DEFAULT_GOAL := build
