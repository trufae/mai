package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	openAIAuthIssuer              = "https://auth.openai.com"
	openAIAuthClientID            = "app_EMoamEEZ73f0CkXaXp7hrann"
	openAIAuthRefreshSkew         = 90 * time.Second
	openAIAuthDefaultPollInterval = 5 * time.Second
)

var openAIAuthMu sync.Mutex

// OpenAIAuthTokens stores Auth0-issued OAuth tokens for OpenAI.
type OpenAIAuthTokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	IDToken      string    `json:"id_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	PlanType     string    `json:"plan_type,omitempty"`
	Email        string    `json:"email,omitempty"`
	ExpiresAt    int64     `json:"expires_at,omitempty"` // Unix seconds
	LastRefresh  time.Time `json:"last_refresh,omitempty"`
}

// OpenAIDeviceCode contains details for the user-driven device auth step.
type OpenAIDeviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        time.Duration
}

type openAIUserCodeResp struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	Interval     json.RawMessage `json:"interval"`
}

type openAITokenPollResp struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

type openAITokenExchangeResp struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type openAIRefreshReq struct {
	ClientID     string `json:"client_id"`
	GrantType    string `json:"grant_type"`
	RefreshToken string `json:"refresh_token"`
}

type openAIRefreshResp struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func getOpenAIAuthFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}
	return filepath.Join(home, ".config", "mai", "openai_auth.json"), nil
}

// OpenAIAuthTokenFilePath returns the path where OpenAI Auth0 tokens are stored.
func OpenAIAuthTokenFilePath() (string, error) {
	return getOpenAIAuthFilePath()
}

func loadOpenAIAuthTokens() (*OpenAIAuthTokens, error) {
	path, err := getOpenAIAuthFilePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	var tokens OpenAIAuthTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	return &tokens, nil
}

func saveOpenAIAuthTokens(tokens *OpenAIAuthTokens) error {
	if tokens == nil {
		return fmt.Errorf("no tokens to save")
	}
	path, err := getOpenAIAuthFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("failed to create auth directory: %w", err)
	}
	payload, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode token file: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(payload); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

func updateOpenAIAuthMetadata(tokens *OpenAIAuthTokens) {
	if tokens == nil {
		return
	}
	if tokens.IDToken != "" {
		if claims, err := parseJWTClaims(tokens.IDToken); err == nil {
			applyJWTClaims(tokens, claims)
		}
	}
	if tokens.AccessToken != "" {
		if claims, err := parseJWTClaims(tokens.AccessToken); err == nil {
			applyJWTClaims(tokens, claims)
		}
	}
}

func parseJWTClaims(jwt string) (map[string]interface{}, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	payload := parts[1]
	decoded, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		// Fallback for payloads that include padding.
		decoded, err = base64.URLEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to decode JWT payload: %w", err)
		}
	}
	dec := json.NewDecoder(bytes.NewReader(decoded))
	dec.UseNumber()
	var claims map[string]interface{}
	if err := dec.Decode(&claims); err != nil {
		return nil, fmt.Errorf("failed to parse JWT payload: %w", err)
	}
	return claims, nil
}

func applyJWTClaims(tokens *OpenAIAuthTokens, claims map[string]interface{}) {
	if tokens == nil || claims == nil {
		return
	}
	if exp := int64Claim(claims["exp"]); exp > 0 {
		tokens.ExpiresAt = exp
	}
	if email := stringClaim(claims["email"]); email != "" {
		tokens.Email = email
	}
	if profile := mapClaim(claims["https://api.openai.com/profile"]); profile != nil {
		if email := stringClaim(profile["email"]); email != "" {
			tokens.Email = email
		}
	}
	if auth := mapClaim(claims["https://api.openai.com/auth"]); auth != nil {
		if accountID := stringClaim(auth["chatgpt_account_id"]); accountID != "" {
			tokens.AccountID = accountID
		}
		if plan := stringClaim(auth["chatgpt_plan_type"]); plan != "" {
			tokens.PlanType = plan
		}
		if tokens.AccountID == "" {
			if org := stringClaim(auth["organization_id"]); org != "" {
				tokens.AccountID = org
			}
		}
	}
}

func mapClaim(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func int64Claim(v interface{}) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n
	default:
		return 0
	}
}

func stringClaim(v interface{}) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func parsePollInterval(raw json.RawMessage) time.Duration {
	if len(raw) == 0 {
		return openAIAuthDefaultPollInterval
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(asString), 10, 64); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	var asNumber int64
	if err := json.Unmarshal(raw, &asNumber); err == nil && asNumber >= 0 {
		return time.Duration(asNumber) * time.Second
	}
	return openAIAuthDefaultPollInterval
}

func doJSONRequest(ctx context.Context, method, endpoint string, body interface{}) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBody, nil
}

func exchangeDeviceCodeForTokens(ctx context.Context, authorizationCode, codeVerifier string) (*OpenAIAuthTokens, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", authorizationCode)
	form.Set("redirect_uri", openAIAuthIssuer+"/deviceauth/callback")
	form.Set("client_id", openAIAuthClientID)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		openAIAuthIssuer+"/oauth/token",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp openAITokenExchangeResp
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token exchange response: %w", err)
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, fmt.Errorf("token exchange response missing access token")
	}
	if strings.TrimSpace(tokenResp.RefreshToken) == "" {
		return nil, fmt.Errorf("token exchange response missing refresh token")
	}

	tokens := &OpenAIAuthTokens{
		IDToken:      strings.TrimSpace(tokenResp.IDToken),
		AccessToken:  strings.TrimSpace(tokenResp.AccessToken),
		RefreshToken: strings.TrimSpace(tokenResp.RefreshToken),
		LastRefresh:  time.Now().UTC(),
	}
	updateOpenAIAuthMetadata(tokens)
	return tokens, nil
}

func isTokenNearExpiry(tokens *OpenAIAuthTokens) bool {
	if tokens == nil || tokens.ExpiresAt <= 0 {
		return false
	}
	expires := time.Unix(tokens.ExpiresAt, 0)
	return time.Now().Add(openAIAuthRefreshSkew).After(expires)
}

func refreshOpenAIAuthTokenLocked(ctx context.Context, tokens *OpenAIAuthTokens) (*OpenAIAuthTokens, error) {
	if tokens == nil {
		return nil, fmt.Errorf("no existing OpenAI auth token data found")
	}
	if strings.TrimSpace(tokens.RefreshToken) == "" {
		return nil, fmt.Errorf("stored OpenAI auth data has no refresh token")
	}

	status, body, err := doJSONRequest(ctx, http.MethodPost, openAIAuthIssuer+"/oauth/token", openAIRefreshReq{
		ClientID:     openAIAuthClientID,
		GrantType:    "refresh_token",
		RefreshToken: tokens.RefreshToken,
	})
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("token refresh failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}

	var refreshResp openAIRefreshResp
	if err := json.Unmarshal(body, &refreshResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}
	if strings.TrimSpace(refreshResp.AccessToken) == "" {
		return nil, fmt.Errorf("refresh response missing access token")
	}

	tokens.AccessToken = strings.TrimSpace(refreshResp.AccessToken)
	if rt := strings.TrimSpace(refreshResp.RefreshToken); rt != "" {
		tokens.RefreshToken = rt
	}
	if idt := strings.TrimSpace(refreshResp.IDToken); idt != "" {
		tokens.IDToken = idt
	}
	tokens.LastRefresh = time.Now().UTC()
	updateOpenAIAuthMetadata(tokens)
	if err := saveOpenAIAuthTokens(tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

// StartOpenAIDeviceCodeLogin starts OpenAI Auth0 device-code login.
func StartOpenAIDeviceCodeLogin(ctx context.Context) (*OpenAIDeviceCode, error) {
	status, body, err := doJSONRequest(ctx, http.MethodPost, openAIAuthIssuer+"/api/accounts/deviceauth/usercode", map[string]string{
		"client_id": openAIAuthClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("device auth request failed: %w", err)
	}
	if status < 200 || status > 299 {
		return nil, fmt.Errorf("device auth request failed with status %d: %s", status, strings.TrimSpace(string(body)))
	}

	var response openAIUserCodeResp
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse device auth response: %w", err)
	}
	if strings.TrimSpace(response.DeviceAuthID) == "" || strings.TrimSpace(response.UserCode) == "" {
		return nil, fmt.Errorf("device auth response missing required fields")
	}

	return &OpenAIDeviceCode{
		VerificationURL: openAIAuthIssuer + "/codex/device",
		UserCode:        strings.TrimSpace(response.UserCode),
		DeviceAuthID:    strings.TrimSpace(response.DeviceAuthID),
		Interval:        parsePollInterval(response.Interval),
	}, nil
}

// CompleteOpenAIDeviceCodeLogin polls for approval, exchanges the code, and persists tokens.
func CompleteOpenAIDeviceCodeLogin(ctx context.Context, device *OpenAIDeviceCode, timeout time.Duration) (*OpenAIAuthTokens, error) {
	if device == nil {
		return nil, fmt.Errorf("missing device auth data")
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	pollInterval := device.Interval
	if pollInterval < 0 {
		pollInterval = openAIAuthDefaultPollInterval
	}

	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		status, body, err := doJSONRequest(ctx, http.MethodPost, openAIAuthIssuer+"/api/accounts/deviceauth/token", map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		})
		if err != nil {
			return nil, fmt.Errorf("device auth polling failed: %w", err)
		}

		if status >= 200 && status <= 299 {
			var pollResp openAITokenPollResp
			if err := json.Unmarshal(body, &pollResp); err != nil {
				return nil, fmt.Errorf("failed to parse device auth token response: %w", err)
			}
			if strings.TrimSpace(pollResp.AuthorizationCode) == "" || strings.TrimSpace(pollResp.CodeVerifier) == "" {
				return nil, fmt.Errorf("device auth token response missing required fields")
			}

			tokens, err := exchangeDeviceCodeForTokens(ctx, pollResp.AuthorizationCode, pollResp.CodeVerifier)
			if err != nil {
				return nil, err
			}
			openAIAuthMu.Lock()
			defer openAIAuthMu.Unlock()
			if err := saveOpenAIAuthTokens(tokens); err != nil {
				return nil, err
			}
			return tokens, nil
		}

		// 403/404 means authorization is still pending.
		if status != http.StatusForbidden && status != http.StatusNotFound {
			return nil, fmt.Errorf("device auth failed with status %d: %s", status, strings.TrimSpace(string(body)))
		}

		if time.Now().After(deadline) {
			return nil, fmt.Errorf("device auth timed out after %s", timeout.String())
		}

		waitFor := pollInterval
		if waitFor < 0 {
			waitFor = 0
		}
		if waitFor == 0 {
			// Avoid a tight loop while still respecting API tests that return interval=0.
			waitFor = 250 * time.Millisecond
		}
		timer := time.NewTimer(waitFor)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// RefreshOpenAIAuthToken refreshes stored OpenAI Auth0 tokens.
func RefreshOpenAIAuthToken(ctx context.Context) (*OpenAIAuthTokens, error) {
	openAIAuthMu.Lock()
	defer openAIAuthMu.Unlock()

	tokens, err := loadOpenAIAuthTokens()
	if err != nil {
		return nil, err
	}
	return refreshOpenAIAuthTokenLocked(ctx, tokens)
}

// GetStoredOpenAIAuthToken returns stored OpenAI Auth0 tokens without refreshing.
func GetStoredOpenAIAuthToken() (*OpenAIAuthTokens, error) {
	openAIAuthMu.Lock()
	defer openAIAuthMu.Unlock()
	return loadOpenAIAuthTokens()
}

// ClearOpenAIAuthToken removes stored OpenAI Auth0 credentials.
func ClearOpenAIAuthToken() error {
	openAIAuthMu.Lock()
	defer openAIAuthMu.Unlock()

	path, err := getOpenAIAuthFilePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete %s: %w", path, err)
	}
	return nil
}

// GetOpenAIAccessToken resolves OpenAI access tokens for bearer auth.
// Priority:
// 1. OPENAI_ACCESS_TOKEN env var
// 2. Stored OpenAI Auth0 token (optionally auto-refreshed)
func GetOpenAIAccessToken(autoRefresh bool) (string, error) {
	if envToken := strings.TrimSpace(os.Getenv("OPENAI_ACCESS_TOKEN")); envToken != "" {
		return envToken, nil
	}

	openAIAuthMu.Lock()
	defer openAIAuthMu.Unlock()

	tokens, err := loadOpenAIAuthTokens()
	if err != nil {
		return "", err
	}
	if tokens == nil || strings.TrimSpace(tokens.AccessToken) == "" {
		return "", nil
	}

	access := strings.TrimSpace(tokens.AccessToken)
	if autoRefresh && isTokenNearExpiry(tokens) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		refreshed, refreshErr := refreshOpenAIAuthTokenLocked(ctx, tokens)
		if refreshErr != nil {
			// Return the current token as best-effort fallback.
			return access, refreshErr
		}
		access = strings.TrimSpace(refreshed.AccessToken)
	}
	return access, nil
}

// ValidateOpenAIAccessToken checks the token against OpenAI's models endpoint.
func ValidateOpenAIAccessToken(ctx context.Context, token string) (int, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, "", fmt.Errorf("no token provided")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.openai.com/v1/models", nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

// IsOpenAIAuthConfigured reports whether either API key or Auth0 access token exists.
func IsOpenAIAuthConfigured() bool {
	if strings.TrimSpace(GetAPIKey("openai")) != "" {
		return true
	}
	token, _ := GetOpenAIAccessToken(false)
	return strings.TrimSpace(token) != ""
}
