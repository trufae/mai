include ../../config.mk
BIN=mai-mcpd-wttr

all:

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BIN) \
		$(DESTDIR)$(PREFIX)/bin/$(BIN)
