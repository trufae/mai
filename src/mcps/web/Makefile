GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

all: build

build:
	go build -o mai-mcp-websearch${EXE} .

clean:
	rm -f mai-mcp-websearch${EXE}

.PHONY: all build clean