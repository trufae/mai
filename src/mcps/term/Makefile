include ../../../config.mk

BINARY_NAME=mai-mcp-term

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

all: $(BINARY_NAME)

$(BINARY_NAME): mai-mcp-term.go
	go build $(BUILD_TAGS) -o $(BINARY_NAME) .

clean:
	rm -f $(BINARY_NAME)

install: all
	mkdir -p $(DESTDIR)$(PREFIX)/bin
	ln -fs $(shell pwd)/$(BINARY_NAME) $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(BINARY_NAME)

.PHONY: all clean install uninstall