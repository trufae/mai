BINARY_NAME=mcpd
MAIN_FILE=main.go

.PHONY: all build run clean deps

all: build

build:
	go build -o $(BINARY_NAME) $(MAIN_FILE)

full f:
	$(MAKE)
	$(MAKE) -C servers/wttr
	$(MAKE) -C clients/mcpd-cli
	./mcpd 'r2pm -r r2mcp' servers/wttr/wttr


run:
	go run $(MAIN_FILE)

clean:
	go clean
	rm -f $(BINARY_NAME)

deps:
	go mod tidy
	go mod download

install:
	go install

test:
	go test ./...

.DEFAULT_GOAL := build
