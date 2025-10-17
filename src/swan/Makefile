BINARY_NAME=mai-swan
BUILD_DIR=./cmd

.PHONY: all build clean fmt test

all: build

build:
	go build -o $(BINARY_NAME) $(BUILD_DIR)

clean:
	rm -f $(BINARY_NAME)

fmt:
	go fmt ./...

test:
	go test ./...

.PHONY: all build clean fmt test