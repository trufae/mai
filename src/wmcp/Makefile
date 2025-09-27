include ../../config.mk

BIN=mai-wmcp
SOURCES=main.go config.go types.go service.go handlers.go

.PHONY: all build run clean deps install uninstall

all: build

build: deps
	go build -o $(BIN) $(SOURCES)

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN) $(DESTDIR)$(PREFIX)/bin/$(BIN)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)

clean:
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

test:
	go test ./...

.DEFAULT_GOAL := build

