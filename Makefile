include config.mk

.PHONY: all run clean deps install uninstall

all:
	$(MAKE) -C src

fmt:
	$(MAKE) -C src/mcps indent
	go fmt $(shell ls src/repl/*.go )
	go fmt $(shell ls src/wmcp/*.go )
	go fmt $(shell ls src/tool/*.go )

install:
	$(MAKE) -C src/wmcp install
	$(MAKE) -C src/repl install
	$(MAKE) -C src/tool install
	$(MAKE) -C src/mcps install
	$(MAKE) -C src/vdb install

uninstall:
	$(MAKE) -C src/wmcp uninstall
	$(MAKE) -C src/repl uninstall
	$(MAKE) -C src/tool uninstall
	$(MAKE) -C src/mcps uninstall
	$(MAKE) -C src/vdb uninstall

run:
	go run $(MAIN_FILE)

mcprun:
	./src/wmcp/mai-wmcp 'r2pm -r r2mcp' src/mcps/wttr/mai-mcp-wttr

clean:
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

test:
	go test ./...

.DEFAULT_GOAL := all
