include ../../config.mk

all:
	go build -o mai-bot

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-bot $(DESTDIR)/$(PREFIX)/bin/mai-bot

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-bot
