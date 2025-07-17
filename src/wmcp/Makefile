include ../../config.mk

BIN=acli-wmcp
MAIN_FILE=main.go

.PHONY: all build run clean deps

all: build

build: deps
	go build -o $(BIN) $(MAIN_FILE)

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN) $(DESTDIR)$(PREFIX)/bin/$(BIN)

clean:
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

test:
	go test ./...

.DEFAULT_GOAL := build

