module mai

go 1.20.0

require github.com/gorilla/mux v1.8.0

require gopkg.in/yaml.v3 v3.0.1

require (
	mcplib v0.0.0-00010101000000-000000000000
	wmcplib v0.0.0
)

replace mcplib => ./src/mcps/lib

replace wmcplib => ./src/wmcp/lib
