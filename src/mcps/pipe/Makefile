GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

all:
	go build -o mai-mcp-pipe${EXE}

clean:
	rm -f mai-mcp-pipe${EXE}
