include ../../config.mk

GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

all:
	go build -o mai-bot${EXE}

clean:
	rm -f mai-bot${EXE}

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-bot${EXE} $(DESTDIR)/$(PREFIX)/bin/mai-bot${EXE}

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-bot${EXE}
