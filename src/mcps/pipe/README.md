# Mai's MCP

Each of these tools is implemented as a single `mai` oneliner, which basically takes the arguments and writes them to the stdin and responds with the output of the request. The configuration file must be in YAML

## Overview
Mai's MCP is a tool management system that enables you to expose and execute various tools through a unified interface. It operates based on a YAML configuration file that defines the available tools and their specifications.

## Configuration

### File Format

The configuration must be provided in YAML format and passed as an argument when running the MCP.

### Tool Definition Structure

Each tool in the configuration file is defined with the following properties:

```yaml
tools:
  tool_name:
    description: "A brief description of what the tool does"
    arguments:
      arg1:
        description: "explanation of the argument1"
        type: "string"
        required: true
      arg2:
        description: "explanation of the argument2"
        type: "string"
        required: false
    command:
      program: "program_name"
      environment:
        ENV_VAR1: "value"
        ENV_VAR2: "value"
      args:
        - "--flag1"
        - "value1"
        - "--flag2"
        - "value2"
```

#### Required Properties:

- `tool_name`: Unique identifier for the tool
- `description`: Brief explanation of the tool's purpose
- `arguments`: Nested mapping defining supported arguments, each with `description`, `type`, and `required` properties
- `command`: Execution specifications
  - `environment`: Environment variables required by the tool
  - `args`: Command-line arguments passed to the tool

## Implementation

Each tool is implemented as a single `program` command that:

1. Accepts the specified arguments
2. Processes them through stdin
3. Returns the output of the execution

## Usage Example

```yaml
# config.yaml
tools:
  word_count:
    description: "Count words"
    arguments:
      content:
        description: "text to use as input"
        required: true
        type: "string"
    command:
      program: "wc"
      environment:
        LANG: "C"
      args:
        - "-w"
```

## Running MCP

```bash
mai mcp config.yaml
```
