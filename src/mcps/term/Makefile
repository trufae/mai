include ../../../config.mk

BINARY_NAME=mai-mcp-term

all: $(BINARY_NAME)

$(BINARY_NAME): mai-mcp-term.go
	go build -o $(BINARY_NAME) .

clean:
	rm -f $(BINARY_NAME)

install: all
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BINARY_NAME) $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)

.PHONY: all clean install uninstall