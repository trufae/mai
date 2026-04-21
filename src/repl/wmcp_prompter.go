package main

import (
	"strings"

	wmcplib "mai/src/wmcp/lib"
)

// replPrompter implements wmcplib.Prompter using mai-repl's configuration
// (mcp.yolo / mcp.yolotools / mcp.denyall / mcp.allowtools / mcp.denytools)
// and falls back to a stdin prompt identical to mai-wmcp's default when a
// decision cannot be made automatically.
type replPrompter struct {
	repl *REPL
	fall *wmcplib.StdinPrompter
}

func newReplPrompter(r *REPL) *replPrompter {
	return &replPrompter{repl: r, fall: wmcplib.NewStdinPrompter()}
}

func (p *replPrompter) conf() *ConfigOptions {
	if p.repl == nil {
		return nil
	}
	return &p.repl.configOptions
}

func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func listContains(list []string, name string) bool {
	for _, s := range list {
		if s == name {
			return true
		}
	}
	return false
}

// AskToolExecution implements wmcplib.Prompter.
func (p *replPrompter) AskToolExecution(toolName, paramsJSON string) wmcplib.YoloDecision {
	cfg := p.conf()
	if cfg == nil {
		return p.fall.AskToolExecution(toolName, paramsJSON)
	}

	if cfg.GetBool("mcp.yolo") {
		return wmcplib.YoloApprove
	}

	deny := splitCSV(cfg.Get("mcp.denytools"))
	if listContains(deny, toolName) {
		return wmcplib.YoloReject
	}

	yolo := splitCSV(cfg.Get("mcp.yolotools"))
	if listContains(yolo, toolName) {
		return wmcplib.YoloApprove
	}

	allow := splitCSV(cfg.Get("mcp.allowtools"))
	if cfg.GetBool("mcp.denyall") {
		if listContains(allow, toolName) {
			return wmcplib.YoloApprove
		}
		return wmcplib.YoloReject
	}

	return p.fall.AskToolExecution(toolName, paramsJSON)
}

// AskPromptExecution implements wmcplib.Prompter.
func (p *replPrompter) AskPromptExecution(promptName, argsJSON string) wmcplib.PromptDecision {
	cfg := p.conf()
	if cfg != nil && cfg.GetBool("mcp.yolo") {
		return wmcplib.PromptApprove
	}
	return p.fall.AskPromptExecution(promptName, argsJSON)
}

// AskToolNotFound implements wmcplib.Prompter.
func (p *replPrompter) AskToolNotFound(toolName string) wmcplib.YoloDecision {
	return p.fall.AskToolNotFound(toolName)
}

// ModifyToolRequest implements wmcplib.Prompter.
func (p *replPrompter) ModifyToolRequest(current *wmcplib.CallToolParams) (*wmcplib.CallToolParams, error) {
	return p.fall.ModifyToolRequest(current)
}

// ReadCustomResponse implements wmcplib.Prompter.
func (p *replPrompter) ReadCustomResponse(message string) (string, error) {
	return p.fall.ReadCustomResponse(message)
}
