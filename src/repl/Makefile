include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

SOURCES = $(wildcard *.go) $(wildcard llm/*.go) $(wildcard art/*.go)

all: mai-repl${EXE}

mai-repl${EXE}: $(SOURCES)
	go build -o $@ .

clean:
	rm -f mai-repl${EXE}

indent fmt:
	go fmt . && cd llm && go fmt .
	go fmt art/*.go

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-repl${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-repl${EXE}
	cd $(DESTDIR)/$(PREFIX)/bin/ && ln -fs mai-repl${EXE} mai${EXE}

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-repl${EXE}
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai${EXE}
