include config.mk

.PHONY: all run clean deps

all:
	$(MAKE) -C src/wmcp
	$(MAKE) -C src/mcps/wttr
	$(MAKE) -C src/repl
	$(MAKE) -C src/tool
	./src/wmcp/mai-wmcp 'r2pm -r r2mcp' src/mcps/wttr/mai-mcp-wttr

fmt:
	go fmt $(shell ls src/repl/*.go )
	go fmt $(shell ls src/wmcp/*.go )
	go fmt $(shell ls src/tool/*.go )
	go fmt $(shell ls src/mcps/wttr/*.go )

install:
	$(MAKE) -C src/wmcp install
	$(MAKE) -C src/repl install
	$(MAKE) -C src/tool install
	$(MAKE) -C src/mcps install

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
