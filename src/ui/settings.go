package main

type Setting struct {
	Name    string
	Value   interface{}
	Type    string // "bool", "string", "combo"
	Options []string
}

func (c *ChatApp) loadSettings() {
	if c.mcpClient == nil {
		return
	}
	result, err := c.mcpClient.CallTool("get_settings", map[string]interface{}{})
	if err != nil {
		c.addErrorMessage("Failed to load settings: " + err.Error())
		return
	}
	if result.IsError {
		c.addErrorMessage("Tool call failed: get_settings")
		return
	}
	// Assume result.Content is []interface{}, first is map[string]interface{}
	if content, ok := result.Content.([]interface{}); ok && len(content) > 0 {
		if settingsMap, ok := content[0].(map[string]interface{}); ok {
			c.settings = nil
			for k, v := range settingsMap {
				s := Setting{Name: k, Value: v}
				if _, ok := v.(bool); ok {
					s.Type = "bool"
				} else if k == "ai.provider" {
					s.Type = "combo"
					s.Options = c.providers
				} else if k == "ai.model" {
					s.Type = "combo"
					s.Options = c.models
				} else {
					s.Type = "string"
				}
				c.settings = append(c.settings, s)
			}
		}
	}
}

func (c *ChatApp) loadProviders() {
	if c.mcpClient == nil {
		return
	}
	result, err := c.mcpClient.CallTool("list_providers", map[string]interface{}{})
	if err != nil {
		c.addErrorMessage("Failed to load providers: " + err.Error())
		return
	}
	if result.IsError {
		c.addErrorMessage("Tool call failed: list_providers")
		return
	}
	// Assume returns []string
	if content, ok := result.Content.([]interface{}); ok && len(content) > 0 {
		if providers, ok := content[0].([]interface{}); ok {
			c.providers = nil
			for _, p := range providers {
				if str, ok := p.(string); ok {
					c.providers = append(c.providers, str)
				}
			}
		}
	}
}

func (c *ChatApp) loadModels() {
	if c.mcpClient == nil {
		return
	}
	result, err := c.mcpClient.CallTool("list_models", map[string]interface{}{})
	if err != nil {
		c.addErrorMessage("Failed to load models: " + err.Error())
		return
	}
	if result.IsError {
		c.addErrorMessage("Tool call failed: list_models")
		return
	}
	// Assume returns []string
	if content, ok := result.Content.([]interface{}); ok && len(content) > 0 {
		if models, ok := content[0].([]interface{}); ok {
			c.models = nil
			for _, m := range models {
				if str, ok := m.(string); ok {
					c.models = append(c.models, str)
				}
			}
		}
	}
}

func (c *ChatApp) setSetting(name string, value interface{}) {
	if c.mcpClient == nil {
		return
	}
	result, err := c.mcpClient.CallTool("set_setting", map[string]interface{}{
		"name":  name,
		"value": value,
	})
	if err != nil {
		c.addErrorMessage("Failed to set setting: " + err.Error())
		return
	}
	if result.IsError {
		c.addErrorMessage("Tool call failed: set_setting")
		return
	}
	// Update local
	for i, s := range c.settings {
		if s.Name == name {
			c.settings[i].Value = value
			break
		}
	}
}
