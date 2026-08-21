include ../../config.mk

BINS=code
BINS+=pancode
BINS+=markdown
BINS+=wttr
BINS+=web
BINS+=pipe
BINS+=shell
BINS+=time
BINS+=term
BINS+=memory
BINS+=deltachat

all:
	for a in $(BINS); do \
		$(MAKE) -C $$a all ; \
	done; \
	wait

clean:
	for a in $(BINS); do \
		$(MAKE) -C $$a clean & \
	done; \
	wait

$(BINS):
	$(MAKE) -C $@ $(MAKECMDGOALS)

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
	gofmt -w $(shell find . -type f -name '*.go')

.PHONY: all clean install uninstall fmt indent
