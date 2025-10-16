DESTDIR?=
PREFIX?=/usr/local

.PHONY: build run clean install uninstall

build:
	swift build --configuration release

run:
	swift run

debug:
	swift run --configuration debug

clean:
	swift package clean

test:
	swift test

fmt format:
	#swift format --in-place --recursive .
	swiftformat Sources/MaiUI/*

install:
	install -d $(DESTDIR)$(PREFIX)/bin
	rm -f $(DESTDIR)$(PREFIX)/bin/mai-ui
	ln -fs $(shell pwd)/.build/release/MaiUI $(DESTDIR)$(PREFIX)/bin/mai-ui
# install .build/release/MaiUI $(DESTDIR)$(PREFIX)/bin/mai-ui

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/mai-ui

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build    - Build the project in release mode"
	@echo "  run      - Run the project"
	@echo "  debug    - Run the project in debug mode"
	@echo "  clean    - Clean build artifacts"
	@echo "  test     - Run tests"
	@echo "  format   - Format code"
	@echo "  install  - Install the executable to $(PREFIX)/bin/mai-ui"
	@echo "  uninstall- Remove the executable from $(PREFIX)/bin/mai-ui"
	@echo "  help     - Show this help message"
