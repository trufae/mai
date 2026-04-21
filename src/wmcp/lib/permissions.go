package wmcplib

// checkToolPermission checks if a tool is allowed to run based on stored permissions
func (s *MCPService) checkToolPermission(toolName string, paramsJSON string) bool {
	s.toolPermsLock.RLock()
	defer s.toolPermsLock.RUnlock()

	if perm, exists := s.toolPerms["y"]; exists && perm.Approved {
		return true
	}

	key := toolName + "#" + paramsJSON
	if perm, exists := s.toolPerms[key]; exists {
		return perm.Approved
	}

	if perm, exists := s.toolPerms[toolName]; exists {
		return perm.Approved
	}

	return false
}

// storeToolPermission stores a tool permission decision
func (s *MCPService) storeToolPermission(toolName string, paramsJSON string, decision YoloDecision) {
	s.toolPermsLock.Lock()
	defer s.toolPermsLock.Unlock()

	switch decision {
	case YoloPermitToolForever:
		s.toolPerms[toolName] = ToolPermission{ToolName: toolName, Approved: true}
	case YoloPermitToolWithParamsForever:
		key := toolName + "#" + paramsJSON
		s.toolPerms[key] = ToolPermission{ToolName: toolName, Parameters: paramsJSON, Approved: true}
	case YoloRejectForever:
		s.toolPerms[toolName] = ToolPermission{ToolName: toolName, Approved: false}
	case YoloPermitAllToolsForever:
		s.toolPerms["y"] = ToolPermission{ToolName: "y", Approved: true}
	}
}

// checkPromptPermission checks if a prompt is allowed to run based on stored permissions
func (s *MCPService) checkPromptPermission(promptName string, argsJSON string) bool {
	s.promptPermsLock.RLock()
	defer s.promptPermsLock.RUnlock()

	if perm, exists := s.promptPerms["y"]; exists && perm.Approved {
		return true
	}

	key := promptName + "#" + argsJSON
	if perm, exists := s.promptPerms[key]; exists {
		return perm.Approved
	}

	if perm, exists := s.promptPerms[promptName]; exists {
		return perm.Approved
	}

	return false
}

// storePromptPermission stores a prompt permission decision
func (s *MCPService) storePromptPermission(promptName, argsJSON string, decision PromptDecision) {
	s.promptPermsLock.Lock()
	defer s.promptPermsLock.Unlock()

	switch decision {
	case PromptPermitPromptForever:
		s.promptPerms[promptName] = PromptPermission{PromptName: promptName, Approved: true}
	case PromptPermitPromptWithArgsForever:
		key := promptName + "#" + argsJSON
		s.promptPerms[key] = PromptPermission{PromptName: promptName, Arguments: argsJSON, Approved: true}
	case PromptRejectForever:
		s.promptPerms[promptName] = PromptPermission{PromptName: promptName, Approved: false}
	case PromptPermitAllPromptsForever:
		s.promptPerms["y"] = PromptPermission{PromptName: "y", Approved: true}
	}
}

// promptToolNotFoundDecision routes through the configured Prompter. In
// non-interactive / yolo modes it short-circuits without asking anyone.
func (s *MCPService) promptToolNotFoundDecision(toolName string) YoloDecision {
	if s.NonInteractive || s.YoloMode || s.prompter == nil {
		return YoloToolNotFound
	}
	return s.prompter.AskToolNotFound(toolName)
}

// AskToolNotFound is the exported entry point into the tool-not-found
// prompt flow, used by the HTTP REST endpoints which don't go through
// ProcessMCPRequest.
func (s *MCPService) AskToolNotFound(toolName string) YoloDecision {
	return s.promptToolNotFoundDecision(toolName)
}

// ModifyTool exposes the Prompter's tool-modification flow to code outside
// the lib (the HTTP REST handlers).
func (s *MCPService) ModifyTool(current *CallToolParams) (*CallToolParams, error) {
	return s.promptModifyTool(current)
}

// ReadCustomResponse exposes the Prompter's free-form reply flow.
func (s *MCPService) ReadCustomResponse(prompt string) (string, error) {
	return s.readCustomResponse(prompt)
}

func (s *MCPService) promptYoloDecision(toolName, paramsJSON string) YoloDecision {
	if s.prompter == nil {
		return YoloReject
	}
	return s.prompter.AskToolExecution(toolName, paramsJSON)
}

func (s *MCPService) promptPromptDecision(promptName, argsJSON string) PromptDecision {
	if s.prompter == nil {
		return PromptReject
	}
	return s.prompter.AskPromptExecution(promptName, argsJSON)
}

func (s *MCPService) promptModifyTool(callParams *CallToolParams) (*CallToolParams, error) {
	if s.prompter == nil {
		return nil, ErrPromptCancelled
	}
	return s.prompter.ModifyToolRequest(callParams)
}

func (s *MCPService) readCustomResponse(message string) (string, error) {
	if s.prompter == nil {
		return "", ErrPromptCancelled
	}
	return s.prompter.ReadCustomResponse(message)
}
