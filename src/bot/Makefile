include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

SOURCES = $(wildcard *.go)

all: mai-bot${EXE}

mai-bot${EXE}: $(SOURCES)
	go build -o $@

clean:
	rm -f mai-bot${EXE}

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-bot${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-bot${EXE}

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-bot${EXE}
