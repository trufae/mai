include ../../config.mk

all:
	go build -o acli-repl .

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/acli-repl $(DESTDIR)/$(PREFIX)/bin/acli-repl
	cd $(DESTDIR)/$(PREFIX)/bin/ && ln -fs acli-repl ai
	cd $(DESTDIR)/$(PREFIX)/bin/ && ln -fs acli-repl acli
