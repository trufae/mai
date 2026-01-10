GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

BUILD_TAGS ?=
ifneq ($(GO_TAGS),)
    BUILD_TAGS = -tags=$(GO_TAGS)
endif

all:
	go build $(BUILD_TAGS) -o mai-mcp-shell${EXE}

clean:
	rm -f mai-mcp-shell${EXE}
