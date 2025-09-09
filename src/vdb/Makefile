include ../../config.mk

all:
	go build -o mai-vdb .

indent fmt:
	go fmt . && go fmt vectordb/*.go

install:
	mkdir -p $(DESTDIR)/$(PREFIX)/bin
	ln -fs $(shell pwd)/mai-vdb $(DESTDIR)/$(PREFIX)/bin/mai-vdb
