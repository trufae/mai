include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

SOURCES = $(wildcard *.go) $(wildcard llm/*.go) $(wildcard art/*.go)

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

all: mai-repl${EXE}

mai-repl${EXE}: $(SOURCES)
	go build $(BUILD_TAGS) -o $@ .

clean:
	rm -f mai-repl${EXE}

indent fmt:
	go fmt . && cd llm && go fmt .
	go fmt art/*.go

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-repl${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-repl${EXE}
	cd $(DESTDIR)/$(PREFIX)/bin/ && ln -fs mai-repl${EXE} mai${EXE}
	mkdir -p $(DESTDIR)/$(PREFIX)/share/mai
	cp -r ../../share/mai/* $(DESTDIR)/$(PREFIX)/share/mai/
	ln -s $(DESTDIR)/$(PREFIX)/share $(DESTDIR)/$(PREFIX)/bin/share
	ln -s $(PREFIX)/share ../../share

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-repl${EXE}
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai${EXE}
