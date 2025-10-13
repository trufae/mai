include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

BIN=mai-tool

SOURCES = $(wildcard *.go)

all: $(BIN)${EXE}

$(BIN)${EXE}: $(SOURCES)
	go build -o $@ .

clean:
	rm -f $(BIN)${EXE}

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN)${EXE} $(DESTDIR)$(PREFIX)/bin/$(BIN)${EXE}

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)${EXE}
