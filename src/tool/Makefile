include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

BIN=mai-tool

SOURCES = $(wildcard *.go)

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

all: $(BIN)${EXE}

$(BIN)${EXE}: $(SOURCES)
	go build $(BUILD_TAGS) -o $@ .

clean:
	rm -f $(BIN)${EXE}

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN)${EXE} $(DESTDIR)$(PREFIX)/bin/$(BIN)${EXE}

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BIN)${EXE}
