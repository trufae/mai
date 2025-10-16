.PHONY: build run clean

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

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build    - Build the project in release mode"
	@echo "  run      - Run the project"
	@echo "  debug    - Run the project in debug mode"
	@echo "  clean    - Clean build artifacts"
	@echo "  test     - Run tests"
	@echo "  format   - Format code"
	@echo "  help     - Show this help message"
