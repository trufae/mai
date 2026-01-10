include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

SOURCES = $(wildcard *.go) $(wildcard vectordb/*.go)

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

all: mai-vdb${EXE}

mai-vdb${EXE}: $(SOURCES)
	go build $(BUILD_TAGS) -o $@ .

clean:
	rm -f mai-vdb${EXE}

indent fmt:
	go fmt . && go fmt vectordb/*.go

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-vdb${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-vdb${EXE}

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-vdb${EXE}
