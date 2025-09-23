# Agentic Coding Guidelines for MAI

## Locations

- Source code of the repl is in `src/repl`
- The Vector Database is in `src/vdb`
- Model Context Protocol library in `src/mcps/lib`

## Coding Style

- Follow the Golang guidelines

## Coding Rules

- Keep changes minimal and take smart decisions

## Actions

- Run `make fmt` to indent the whole reposistory
- Compile your changes with: `make -j > /dev/null`
  - Run `make` in the working directory where you made the changes to avoid recompiling everything
  - We assume system-wide installations via symlinks by default, so there's no need to install after compiling for testing
- Use the `-c debug=true` option to see verbose debug statements when testing oneliners
