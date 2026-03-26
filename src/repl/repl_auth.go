package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/trufae/mai/src/repl/llm"
)

func registerAuthCommands(r *REPL) {
	r.commands["/auth"] = Command{
		Name:        "/auth",
		Description: "Manage OpenAI Auth0 login (status, login, refresh, logout)",
		Handler: func(r *REPL, args []string) (string, error) {
			return r.handleAuthCommand(args)
		},
	}
}

func (r *REPL) handleAuthCommand(args []string) (string, error) {
	action := "status"
	if len(args) >= 2 {
		action = strings.ToLower(strings.TrimSpace(args[1]))
	}

	switch action {
	case "help":
		return "Usage: /auth [status|check|login|refresh|logout]\r\n", nil
	case "status":
		return r.authStatus(false)
	case "check":
		return r.authStatus(true)
	case "login":
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		deviceCode, err := llm.StartOpenAIDeviceCodeLogin(ctx)
		if err != nil {
			return fmt.Sprintf("OpenAI login failed to start: %v\r\n", err), nil
		}

		fmt.Printf("Open this URL in your browser:\r\n  %s\r\n", deviceCode.VerificationURL)
		fmt.Printf("Enter this one-time code:\r\n  %s\r\n", deviceCode.UserCode)
		fmt.Printf("Waiting for authorization (timeout: 15m)...\r\n")

		tokens, err := llm.CompleteOpenAIDeviceCodeLogin(ctx, deviceCode, 15*time.Minute)
		if err != nil {
			return fmt.Sprintf("OpenAI login failed: %v\r\n", err), nil
		}

		var output strings.Builder
		output.WriteString("OpenAI Auth0 login successful.\r\n")
		if tokens.AccountID != "" {
			fmt.Fprintf(&output, "Account: %s\r\n", tokens.AccountID)
		}
		if tokens.PlanType != "" {
			fmt.Fprintf(&output, "Plan: %s\r\n", tokens.PlanType)
		}
		if tokens.ExpiresAt > 0 {
			fmt.Fprintf(&output, "Access token expires at: %s\r\n", time.Unix(tokens.ExpiresAt, 0).Format(time.RFC3339))
		}
		output.WriteString("OpenAI provider can now use Auth0 access tokens.\r\n")
		return output.String(), nil
	case "refresh":
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		tokens, err := llm.RefreshOpenAIAuthToken(ctx)
		if err != nil {
			return fmt.Sprintf("OpenAI token refresh failed: %v\r\n", err), nil
		}
		var output strings.Builder
		output.WriteString("OpenAI token refresh successful.\r\n")
		if tokens.ExpiresAt > 0 {
			fmt.Fprintf(&output, "New expiry: %s\r\n", time.Unix(tokens.ExpiresAt, 0).Format(time.RFC3339))
		}
		return output.String(), nil
	case "logout":
		if err := llm.ClearOpenAIAuthToken(); err != nil {
			return fmt.Sprintf("OpenAI logout failed: %v\r\n", err), nil
		}
		return "OpenAI Auth0 token cleared.\r\n", nil
	default:
		return "Usage: /auth [status|check|login|refresh|logout]\r\n", nil
	}
}

func (r *REPL) authStatus(validate bool) (string, error) {
	var output strings.Builder
	fmt.Fprintf(&output, "Current provider: %s\r\n", r.configOptions.Get("ai.provider"))

	if apiKey := llm.GetAPIKey("openai"); apiKey != "" {
		output.WriteString("OPENAI_API_KEY configured: yes (takes precedence over Auth0 token)\r\n")
	} else {
		output.WriteString("OPENAI_API_KEY configured: no\r\n")
	}

	tokenPath, pathErr := llm.OpenAIAuthTokenFilePath()
	tokens, err := llm.GetStoredOpenAIAuthToken()
	if err != nil {
		fmt.Fprintf(&output, "Auth0 token state: error (%v)\r\n", err)
		if pathErr == nil {
			fmt.Fprintf(&output, "Token file: %s\r\n", tokenPath)
		}
		return output.String(), nil
	}
	if tokens == nil || strings.TrimSpace(tokens.AccessToken) == "" {
		output.WriteString("Auth0 token state: not logged in\r\n")
		output.WriteString("Run '/auth login' to authenticate with OpenAI Auth0.\r\n")
		if pathErr == nil {
			fmt.Fprintf(&output, "Token file: %s\r\n", tokenPath)
		}
		return output.String(), nil
	}

	output.WriteString("Auth0 token state: logged in\r\n")
	if tokens.AccountID != "" {
		fmt.Fprintf(&output, "Account: %s\r\n", tokens.AccountID)
	}
	if tokens.PlanType != "" {
		fmt.Fprintf(&output, "Plan: %s\r\n", tokens.PlanType)
	}
	if tokens.Email != "" {
		fmt.Fprintf(&output, "Email: %s\r\n", tokens.Email)
	}
	if !tokens.LastRefresh.IsZero() {
		fmt.Fprintf(&output, "Last refresh: %s\r\n", tokens.LastRefresh.Format(time.RFC3339))
	}
	if tokens.ExpiresAt > 0 {
		exp := time.Unix(tokens.ExpiresAt, 0)
		if time.Now().After(exp) {
			fmt.Fprintf(&output, "Access token expiry: %s (expired)\r\n", exp.Format(time.RFC3339))
		} else {
			fmt.Fprintf(&output, "Access token expiry: %s\r\n", exp.Format(time.RFC3339))
		}
	} else {
		output.WriteString("Access token expiry: unknown\r\n")
	}
	if pathErr == nil {
		fmt.Fprintf(&output, "Token file: %s\r\n", tokenPath)
	}

	if validate {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		statusCode, body, err := llm.ValidateOpenAIAccessToken(ctx, tokens.AccessToken)
		if err != nil {
			fmt.Fprintf(&output, "Validation: request failed (%v)\r\n", err)
		} else {
			fmt.Fprintf(&output, "Validation HTTP status: %d\r\n", statusCode)
			if body != "" {
				if len(body) > 200 {
					body = body[:200] + "..."
				}
				fmt.Fprintf(&output, "Validation body: %s\r\n", body)
			}
		}
	} else {
		output.WriteString("Use '/auth check' to validate the token against OpenAI.\r\n")
	}

	return output.String(), nil
}
