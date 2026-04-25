package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	wmcplib "mai/src/wmcp/lib"
)

// embedService is the in-process wmcp service used when mcp.transport is
// "embed". A single instance is created lazily and reused across calls; the
// repl's lifecycle owns stopping it on shutdown.
var (
	embedService     *wmcplib.MCPService
	embedServiceOnce sync.Mutex
	embedActiveRepl  *REPL
)

// embedTransportActive reports whether the active config wants the embedded
// in-process wmcp library instead of shelling out to the mai-wmcp / mai-tool
// binaries.
func embedTransportActive(co *ConfigOptions) bool {
	if co == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(co.Get("mcp.transport")), "embed")
}

// replEmbedActive checks the same flag against a REPL's config.
func replEmbedActive(r *REPL) bool {
	if r == nil {
		return false
	}
	return embedTransportActive(&r.configOptions)
}

// embedGetService returns the embedded MCPService, constructing it on first
// use. The repl pointer is only used to wire the Prompter back into the TUI.
func embedGetService(r *REPL) (*wmcplib.MCPService, error) {
	embedServiceOnce.Lock()
	defer embedServiceOnce.Unlock()

	if embedService != nil {
		embedActiveRepl = r
		return embedService, nil
	}

	cfg, err := embedLoadConfig(r)
	if err != nil {
		return nil, err
	}

	yolo := false
	debug := false
	proxy := false
	if r != nil {
		yolo = r.configOptions.GetBool("mcp.yolo")
		debug = r.configOptions.GetBool("mcp.debug")
		proxy = r.configOptions.GetBool("mcp.proxytools")
	}

	service := wmcplib.NewMCPService(wmcplib.Options{
		YoloMode:       yolo || cfg.MaiOptions.YoloMode,
		DrunkMode:      cfg.MaiOptions.DrunkMode,
		ReportFile:     cfg.MaiOptions.OutputReport,
		NoPrompts:      cfg.MaiOptions.NoPrompts,
		NonInteractive: cfg.MaiOptions.NonInteractive,
		SessionMode:    cfg.MaiOptions.SessionMode,
		DebugMode:      debug || cfg.MaiOptions.DebugMode,
		ProxyToolsMode: proxy || cfg.MaiOptions.ProxyToolsMode,
		Prompter:       newReplPrompter(r),
	})

	if len(cfg.MCPServers) > 0 {
		wmcplib.StartMCPServersFromConfig(service, cfg)
	}

	if len(service.Servers) == 0 {
		return nil, fmt.Errorf("embed transport: no MCP servers configured in %s", embedConfigPath(r))
	}

	embedService = service
	embedActiveRepl = r
	return service, nil
}

// embedLoadConfig resolves the MCP config file the same way mai-wmcp's main
// does so users keep a single source of truth.
func embedLoadConfig(r *REPL) (*wmcplib.Config, error) {
	path := embedConfigPath(r)

	if path == "" {
		return nil, fmt.Errorf("no MCP config path available")
	}

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return &wmcplib.Config{MCPServers: map[string]wmcplib.MCPServerConfig{}}, nil
		}
		return nil, err
	}

	if cfg, err := wmcplib.LoadMAIConfig(path); err == nil && len(cfg.MCPServers) > 0 {
		return cfg, nil
	}

	return wmcplib.LoadConfig(path)
}

func embedConfigPath(r *REPL) string {
	if r != nil {
		if p := strings.TrimSpace(r.configOptions.Get("mcp.config")); p != "" {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mai", "mcps.json")
}

// embedStop disposes the embedded service (called on repl shutdown).
func embedStop() {
	embedServiceOnce.Lock()
	defer embedServiceOnce.Unlock()

	if embedService != nil {
		embedService.StopAllServers()
		embedService = nil
	}
	embedActiveRepl = nil
}

// embedListToolsFormatted returns the aggregated tool catalog in one of the
// formats mai-tool supports, without going through mai-tool. The formatting
// is kept deliberately compatible with the existing consumers.
func embedListToolsFormatted(r *REPL, f Format) (string, error) {
	svc, err := embedGetService(r)
	if err != nil {
		return "", err
	}

	svc.Mutex.RLock()
	defer svc.Mutex.RUnlock()

	if svc.ProxyToolsMode {
		return embedFormatProxyTools(f), nil
	}

	switch f {
	case JSON:
		res := make(map[string][]wmcplib.Tool)
		for serverName, server := range svc.Servers {
			server.Mutex.RLock()
			tools := make([]wmcplib.Tool, len(server.Tools))
			copy(tools, server.Tools)
			for i := range tools {
				if len(tools[i].Parameters) == 0 && tools[i].InputSchema != nil {
					tools[i].Parameters = wmcplib.ExtractParametersFromSchema(tools[i].InputSchema)
				}
			}
			server.Mutex.RUnlock()
			res[serverName] = tools
		}
		b, err := json.Marshal(res)
		if err != nil {
			return "", err
		}
		return string(b), nil

	case Quiet:
		return embedFormatQuiet(svc), nil

	case XML:
		return embedFormatXML(svc), nil

	case Simple:
		return embedFormatSimple(svc), nil

	case Markdown:
		return embedFormatMarkdown(svc), nil
	}

	return embedFormatMarkdown(svc), nil
}

// embedFormatProxyTools renders the two virtual proxy tools in the format
// requested by the REPL. Used when the embedded service has ProxyToolsMode
// enabled — the real underlying tools must not appear in the LLM-facing
// catalog.
func embedFormatProxyTools(f Format) string {
	tools := wmcplib.ProxyTools()
	switch f {
	case JSON:
		b, _ := json.Marshal(map[string][]wmcplib.Tool{"proxy": tools})
		return string(b)
	case XML:
		var out strings.Builder
		out.WriteString("<tools>\n")
		for _, tool := range tools {
			out.WriteString(fmt.Sprintf("  <tool server=%q name=%q>\n", "proxy", tool.Name))
			out.WriteString(fmt.Sprintf("    <description>%s</description>\n", tool.Description))
			for _, p := range tool.Parameters {
				required := ""
				if p.Required {
					required = " required=\"true\""
				}
				out.WriteString(fmt.Sprintf("    <param name=%q type=%q%s>%s</param>\n",
					p.Name, p.Type, required, p.Description))
			}
			out.WriteString("  </tool>\n")
		}
		out.WriteString("</tools>\n")
		return out.String()
	case Simple:
		var out strings.Builder
		notFirst := false
		for _, tool := range tools {
			if notFirst {
				out.WriteString("--\n")
			}
			notFirst = true
			out.WriteString(fmt.Sprintf("TOOLNAME: %s\n", tool.Name))
			out.WriteString(fmt.Sprintf("DESCRIPTION: %s\n", tool.Description))
			if len(tool.Parameters) > 0 {
				var examples []string
				for _, p := range tool.Parameters {
					examples = append(examples, fmt.Sprintf("%s=<value>", p.Name))
				}
				out.WriteString(fmt.Sprintf("USAGE: proxy %s %s\n", tool.Name, strings.Join(examples, " ")))
			}
		}
		return out.String()
	case Quiet:
		var out strings.Builder
		for _, tool := range tools {
			out.WriteString(fmt.Sprintf("- ToolName: %s\n", tool.Name))
			if tool.Description != "" {
				out.WriteString(fmt.Sprintf("  Description: %s\n", tool.Description))
			}
			if len(tool.Parameters) > 0 {
				out.WriteString("  Parameters:\n")
				for _, p := range tool.Parameters {
					req := ""
					if p.Required {
						req = " [required]"
					}
					out.WriteString(fmt.Sprintf("  - %s=<%s> : %s%s\n", p.Name, p.Type, p.Description, req))
				}
			}
		}
		return strings.TrimRight(out.String(), "\n")
	}

	var out strings.Builder
	out.WriteString("# Tools Catalog\n\n")
	out.WriteString("## Proxy Tools Mode\n\n")
	out.WriteString("Only two virtual tools are exposed; they gate access to the real underlying tools.\n\n")
	for _, tool := range tools {
		out.WriteString(fmt.Sprintf("### %s\n", tool.Name))
		out.WriteString(fmt.Sprintf("**Description:** %s\n\n", tool.Description))
		if len(tool.Parameters) > 0 {
			out.WriteString("**Parameters:**\n")
			for _, p := range tool.Parameters {
				req := ""
				if p.Required {
					req = " (required)"
				}
				out.WriteString(fmt.Sprintf("- %s (%s)%s: %s\n", p.Name, p.Type, req, p.Description))
			}
			out.WriteString("\n")
		}
	}
	return out.String()
}

func embedFormatQuiet(svc *wmcplib.MCPService) string {
	categoryOrder := []string{"File", "Analysis", "Inspection", "Metadata", "Editing"}
	toolsByCategory := make(map[string][]wmcplib.QuietToolEntry)

	names := make([]string, 0, len(svc.Servers))
	for n := range svc.Servers {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		server := svc.Servers[n]
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			entry := wmcplib.BuildQuietToolEntry(n, tool)
			cat := entry.Category
			if cat == "" {
				cat = "Analysis"
			}
			toolsByCategory[cat] = append(toolsByCategory[cat], entry)
		}
		server.Mutex.RUnlock()
	}

	var output strings.Builder
	first := true
	for _, category := range categoryOrder {
		entries := toolsByCategory[category]
		if len(entries) == 0 {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Server == entries[j].Server {
				return entries[i].Name < entries[j].Name
			}
			return entries[i].Server < entries[j].Server
		})
		if !first {
			output.WriteByte('\n')
		}
		first = false
		for _, e := range entries {
			output.WriteString(fmt.Sprintf("- ToolName: %s\n", e.Name))
			if e.Purpose != "" {
				output.WriteString(fmt.Sprintf("  Description: %s", e.Purpose))
				if e.WhenToUse != "" {
					output.WriteString(fmt.Sprintf(" (%s)\n", e.WhenToUse))
				} else {
					output.WriteString("\n")
				}
			}
			if len(e.Args) > 0 {
				output.WriteString("  Parameters:\n")
				for _, arg := range e.Args {
					output.WriteString(wmcplib.FormatQuietArgument(arg))
					output.WriteByte('\n')
				}
			}
		}
	}
	return strings.TrimRight(output.String(), "\n")
}

func embedFormatSimple(svc *wmcplib.MCPService) string {
	var output strings.Builder
	for serverName, server := range svc.Servers {
		server.Mutex.RLock()
		notFirst := false
		for _, tool := range server.Tools {
			if notFirst {
				output.WriteString("--\n")
			}
			notFirst = true
			output.WriteString(fmt.Sprintf("TOOLNAME: %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("DESCRIPTION: %s\n", tool.Description))

			if len(tool.Parameters) > 0 {
				var mandatory, optional, paramExamples []string
				for _, param := range tool.Parameters {
					paramExamples = append(paramExamples, fmt.Sprintf("%s=<value>", param.Name))
					paramDesc := fmt.Sprintf("%s (%s)", param.Name, param.Type)
					if param.Required {
						mandatory = append(mandatory, paramDesc)
					} else {
						optional = append(optional, paramDesc)
					}
				}
				output.WriteString(fmt.Sprintf("USAGE: %s %s %s\n", serverName, tool.Name, strings.Join(paramExamples, " ")))
				if len(mandatory) > 0 {
					output.WriteString("MANDATORY PARAMS:")
					for _, p := range mandatory {
						output.WriteString(" " + p)
					}
					output.WriteString("\n")
				}
				if len(optional) > 0 {
					output.WriteString("OPTIONAL PARAMS:")
					for _, p := range optional {
						output.WriteString(" " + p)
					}
					output.WriteString("\n")
				}
			}
		}
		server.Mutex.RUnlock()
	}
	return output.String()
}

func embedFormatXML(svc *wmcplib.MCPService) string {
	var output strings.Builder
	output.WriteString("<tools>\n")
	for serverName, server := range svc.Servers {
		server.Mutex.RLock()
		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("  <tool server=%q name=%q>\n", serverName, tool.Name))
			output.WriteString(fmt.Sprintf("    <description>%s</description>\n", tool.Description))
			for _, p := range tool.Parameters {
				required := ""
				if p.Required {
					required = " required=\"true\""
				}
				output.WriteString(fmt.Sprintf("    <param name=%q type=%q%s>%s</param>\n",
					p.Name, p.Type, required, p.Description))
			}
			output.WriteString("  </tool>\n")
		}
		server.Mutex.RUnlock()
	}
	output.WriteString("</tools>\n")
	return output.String()
}

func embedFormatMarkdown(svc *wmcplib.MCPService) string {
	var output strings.Builder
	output.WriteString("# Tools Catalog\n\n")
	for serverName, server := range svc.Servers {
		server.Mutex.RLock()
		output.WriteString(fmt.Sprintf("## Server: %s\n", serverName))
		output.WriteString(fmt.Sprintf("Tools: %d\n\n", len(server.Tools)))
		for _, tool := range server.Tools {
			output.WriteString(fmt.Sprintf("### %s\n", tool.Name))
			output.WriteString(fmt.Sprintf("**Description:** %s\n\n", tool.Description))
			if len(tool.Parameters) > 0 {
				output.WriteString("**Parameters:**\n")
				for _, param := range tool.Parameters {
					req := ""
					if param.Required {
						req = " (required)"
					}
					output.WriteString(fmt.Sprintf("- %s (%s)%s: %s\n", param.Name, param.Type, req, param.Description))
				}
				output.WriteString("\n")
			}
		}
		server.Mutex.RUnlock()
	}
	return output.String()
}

// embedCallTool routes a tool invocation through the in-process service
// using the same semantics the HTTP bridge would: arguments map, result
// rendered as plain text.
func embedCallTool(r *REPL, toolName string, args map[string]interface{}, timeoutSeconds int) (string, error) {
	svc, err := embedGetService(r)
	if err != nil {
		return "", err
	}

	_ = timeoutSeconds // the library imposes its own 30s stdio timeout for now

	// In proxy mode the agent may only call the two virtual tools; route them
	// through ProcessMCPRequest which knows how to handle them.
	if svc.ProxyToolsMode {
		req := wmcplib.JSONRPCRequest{
			JSONRPC: "2.0",
			Method:  "tools/call",
			Params:  wmcplib.CallToolParams{Name: toolName, Arguments: args},
			ID:      time.Now().UnixNano(),
		}
		resp, _ := svc.ProcessMCPRequest(req)
		if resp == nil {
			return "", nil
		}
		if resp.Error != nil {
			return "", fmt.Errorf("%v", resp.Error)
		}
		return renderCallToolResult(resp.Result)
	}

	server, resolvedName, err := svc.ResolveTool(toolName)
	if err != nil {
		return "", err
	}

	req := wmcplib.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params:  wmcplib.CallToolParams{Name: resolvedName, Arguments: args},
		ID:      time.Now().UnixNano(),
	}

	resp, err := svc.SendRequest(server, req)
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%v", resp.Error)
	}

	return renderCallToolResult(resp.Result)
}

// renderCallToolResult converts an MCP tools/call Result payload into the
// plain-text representation the REPL expects (concatenated content text, or
// raw JSON when the payload doesn't fit the CallToolResult shape).
func renderCallToolResult(result interface{}) (string, error) {
	resultBytes, _ := json.Marshal(result)
	var toolResult wmcplib.CallToolResult
	if err := json.Unmarshal(resultBytes, &toolResult); err != nil {
		return string(resultBytes), nil
	}
	if toolResult.Error != nil {
		return "", fmt.Errorf("%s", toolResult.Error.Message)
	}

	var output strings.Builder
	for i, c := range toolResult.Content {
		if i > 0 {
			output.WriteString("\n\n")
		}
		output.WriteString(c.Text)
	}
	return output.String(), nil
}

// embedCallToolStringArgs mirrors the "key=value" argument syntax mai-tool
// accepts on the command line, so the existing REPL call sites can drop in
// with no argument-shape translation.
func embedCallToolStringArgs(r *REPL, toolName string, kvArgs []string, timeoutSeconds int) (string, error) {
	args := make(map[string]interface{}, len(kvArgs))
	for _, kv := range kvArgs {
		if !strings.Contains(kv, "=") {
			continue
		}
		parts := strings.SplitN(kv, "=", 2)
		args[parts[0]] = parts[1]
	}
	return embedCallTool(r, toolName, args, timeoutSeconds)
}

// embedListPromptsSimple lists prompts across all servers (server/prompt
// form), matching what `mai-tool prompts list` returns in simple mode.
func embedListPromptsSimple(r *REPL) (string, error) {
	svc, err := embedGetService(r)
	if err != nil {
		return "", err
	}
	svc.Mutex.RLock()
	defer svc.Mutex.RUnlock()

	var output strings.Builder
	for serverName, server := range svc.Servers {
		server.Mutex.RLock()
		for _, p := range server.Prompts {
			fmt.Fprintf(&output, "%s/%s\n", serverName, p.Name)
		}
		server.Mutex.RUnlock()
	}
	return output.String(), nil
}

// embedGetPrompt calls prompts/get for a prompt name (possibly prefixed with
// server/). Returns the concatenated text content of the returned messages.
func embedGetPrompt(r *REPL, promptName string, args map[string]interface{}) (string, error) {
	svc, err := embedGetService(r)
	if err != nil {
		return "", err
	}

	name := promptName
	// Accept "server/prompt" in addition to the lib's "server::prompt" form.
	if strings.Contains(name, "/") && !strings.Contains(name, wmcplib.AggregatedNameSeparator) {
		parts := strings.SplitN(name, "/", 2)
		name = parts[0] + wmcplib.AggregatedNameSeparator + parts[1]
	}

	server, resolvedName, err := svc.ResolvePrompt(name)
	if err != nil {
		return "", err
	}

	req := wmcplib.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "prompts/get",
		Params:  wmcplib.GetPromptParams{Name: resolvedName, Arguments: args},
		ID:      time.Now().UnixNano(),
	}
	resp, err := svc.SendRequest(server, req)
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		return "", fmt.Errorf("%v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result wmcplib.GetPromptResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return string(resultBytes), nil
	}

	var output strings.Builder
	for _, m := range result.Messages {
		for _, c := range m.Content {
			output.WriteString(c.Text)
			output.WriteString("\n")
		}
	}
	return strings.TrimRight(output.String(), "\n"), nil
}
