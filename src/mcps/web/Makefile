GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

all: build

build:
	go build $(BUILD_TAGS) -o mai-mcp-websearch${EXE} .

clean:
	rm -f mai-mcp-websearch${EXE}

.PHONY: all build clean