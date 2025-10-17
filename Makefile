include config.mk

.PHONY: all run clean deps install uninstall

all:
	$(MAKE) -C src

.PHONY: v ver version
v ver version:
	@grep Version src/repl/version.go|cut -d '"' -f2

fmt:
	$(MAKE) -C src/mcps indent
	go fmt $(shell ls src/repl/*.go )
	go fmt $(shell ls src/wmcp/*.go )
	go fmt $(shell ls src/tool/*.go )

install:
	$(MAKE) -C src/wmcp install
	$(MAKE) -C src/repl install
	$(MAKE) -C src/tool install
	$(MAKE) -C src/swan install
	$(MAKE) -C src/mcps install
	$(MAKE) -C src/vdb install
	$(MAKE) -C src/bot install

uninstall:
	$(MAKE) -C src/wmcp uninstall
	$(MAKE) -C src/repl uninstall
	$(MAKE) -C src/tool uninstall
	$(MAKE) -C src/mcps uninstall
	$(MAKE) -C src/swan uninstall
	$(MAKE) -C src/vdb uninstall
	$(MAKE) -C src/bot uninstall

run:
	go run $(MAIN_FILE)

mcprun:
	./src/wmcp/mai-wmcp 'r2pm -r r2mcp' src/mcps/wttr/mai-mcp-wttr

clean:
	$(MAKE) -C src clean
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

test:
	go test ./...

.DEFAULT_GOAL := all
