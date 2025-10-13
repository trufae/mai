include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

SOURCES = $(wildcard *.go) $(wildcard vectordb/*.go)

all: mai-vdb${EXE}

mai-vdb${EXE}: $(SOURCES)
	go build -o $@ .

clean:
	rm -f mai-vdb${EXE}

indent fmt:
	go fmt . && go fmt vectordb/*.go

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-vdb${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-vdb${EXE}

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-vdb${EXE}
