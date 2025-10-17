include ../../config.mk
BINARY_NAME=mai-swan
BUILD_DIR=./cmd

.PHONY: all build clean fmt test

all: build

build:
	go build -o $(BINARY_NAME) $(BUILD_DIR)

run:
	-pkill mai-swan
	./mai-swan -w /tmp/swandb -d -c ./swan.yaml

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-swan${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-swan${EXE}

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai${EXE}

clean:
	rm -f $(BINARY_NAME)

fmt:
	go fmt ./...

test:
	go test ./...

.PHONY: all build clean fmt test
