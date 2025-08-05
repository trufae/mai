include ../../config.mk

BINS=code
BINS+=gemcode
BINS+=wttr
BINS+=pipe
BINS+=shell
BINS+=time

all:

install:
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	for a in $(BINS); do \
		ln -fs $(shell pwd)/$$a/mai-mcp-$$a \
			$(DESTDIR)$(PREFIX)/bin/mai-mcp-$$a ; \
	done
