DESTDIR?=
PREFIX?=/usr/local
MAIUI=./build/mai-gnome

build:
	meson setup build --prefix=/usr || true
	ninja -C build

run: build
	$(MAIUI)

clean:
	rm -rf build

.PHONY: build run clean install uninstall

install:
	install -d $(DESTDIR)$(PREFIX)/bin
	rm -f $(DESTDIR)$(PREFIX)/bin/mai-ui
	ln -fs $(shell pwd)/$(MAIUI) $(DESTDIR)$(PREFIX)/bin/mai-ui
# install .build/release/MaiUI $(DESTDIR)$(PREFIX)/bin/mai-ui

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/mai-ui

