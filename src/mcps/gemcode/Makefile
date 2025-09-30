GOOS ?= $(shell go env GOOS)
EXE = $(if $(filter windows,${GOOS}),.exe,)

all:
	go build -o mai-mcp-gemcode${EXE}
