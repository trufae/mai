build:
	meson setup build --prefix=/usr || true
	ninja -C build

run: build
	./build/mai-gnome

clean:
	rm -rf build

.PHONY: build run clean
