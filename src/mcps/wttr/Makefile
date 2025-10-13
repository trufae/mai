GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

all:
	go build -o mai-mcp-wttr${EXE} .

clean:
	rm -f mai-mcp-wttr${EXE}
