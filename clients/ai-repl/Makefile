DESTDIR?=
PREFIX?=/usr/local

all:
	go build .

install:
	sudo ln -fs $(shell pwd)/ai-repl $(DESTDIR)/$(PREFIX)/bin/ai

