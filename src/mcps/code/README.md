# MCP Code Service

The MCP Code Service is a Go-based implementation that provides various code-related operations. This service allows users to analyze, compile, and run code across different programming languages and build systems.

## Features

### Code Analysis
- **Language Identification**: Automatically detect programming languages used in files or projects.
- **Build System Detection**: Identify build systems (Make, CMake, Meson, npm, etc.) in project directories.
- **Function Analysis**: List functions in source code files and extract function bodies.
- **Structure Analysis**: Query classes, interfaces, structs, and other code structures.

### Build and Run
- **Compilation**: Compile source code files or projects using appropriate build systems.
- **Execution**: Run programs with customizable arguments.
- **Meson Support**: Specialized support for Meson projects, including builddir management.

### File Operations
- **File Change Tracking**: Track if files have been modified since last check.
- **Patch Application**: Apply patches to source files by replacing specific line ranges.
- **Basic File Operations**: Read, write, append, delete, move, rename, and copy files.

### System Operations
- **Command Execution**: Execute shell commands with optional timeout.
- **Environment Variables**: Get and set environment variables.
- **Directory Operations**: Create, remove, list, and change directories.

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

## License

This project is licensed under the MIT License.

## Contact

For more information, please contact pancake@nopcode.org.
