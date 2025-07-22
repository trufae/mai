include ../../config.mk

BIN=mai-tool

all:
	go build -o $(BIN) .

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN) $(DESTDIR)$(PREFIX)/bin/$(BIN)
