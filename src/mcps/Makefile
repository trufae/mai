include ../../config.mk

BINS=code
BINS+=pancode
BINS+=markdown
BINS+=wttr
BINS+=pipe
BINS+=shell
BINS+=time
BINS+=term

all clean:
	for a in $(BINS); do $(MAKE) -C $$a $< || exit 1; done

install: all
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	for a in $(BINS); do \
		ln -fs $(shell pwd)/$$a/mai-mcp-$$a \
			$(DESTDIR)$(PREFIX)/bin/mai-mcp-$$a ; \
	done

uninstall:
	for a in $(BINS); do \
		rm -f $(DESTDIR)$(PREFIX)/bin/mai-mcp-$$a ; \
	done

fmt indent:
	for a in $(shell ls -F | grep /) ; do ( go fmt $$a/*.go ) ; done

.PHONY: all clean install uninstall fmt indent
