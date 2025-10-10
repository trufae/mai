module github.com/trufae/mai/src/ui

go 1.23.8

require (
	gioui.org v0.9.0
	gioui.org/x v0.9.0
	mcplib v0.0.0
)

replace mcplib => ../mcps/lib

require (
	gioui.org/shader v1.0.8 // indirect
	github.com/go-text/typesetting v0.3.0 // indirect
	github.com/yuin/goldmark v1.4.13 // indirect
	golang.org/x/exp v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/exp/shiny v0.0.0-20250408133849-7e4ce0ab07d0 // indirect
	golang.org/x/image v0.26.0 // indirect
	golang.org/x/sys v0.33.0 // indirect
	golang.org/x/text v0.24.0 // indirect
)
