include ../../config.mk

all:
	go build -o mai-repl .

indent fmt:
	go fmt . && cd llm && go fmt .

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-repl $(DESTDIR)/$(PREFIX)/bin/mai-repl
	cd $(DESTDIR)/$(PREFIX)/bin/ && ln -fs mai-repl mai

uninstall:
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai-repl
	rm -f $(DESTDIR)/$(PREFIX)/bin/mai
