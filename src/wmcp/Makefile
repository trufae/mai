include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

BIN=mai-wmcp
SOURCES=main.go config.go types.go service.go handlers.go mcp_http.go utils.go tools.go permissions.go servers.go

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

.PHONY: all build run clean deps install uninstall

all: build

build: deps
	go build $(BUILD_TAGS) -o $(BIN)${EXE} $(SOURCES)

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN)${EXE} $(DESTDIR)$(PREFIX)/bin/$(BIN)${EXE}

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)${EXE}

clean:
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

test:
	go test ./...

.DEFAULT_GOAL := build

