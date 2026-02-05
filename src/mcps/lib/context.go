package mcplib

import "context"

type contextKey string

const (
	ContextKeyAPIToken contextKey = "api_token"
)

func GetAPIToken(ctx context.Context) string {
	if v := ctx.Value(ContextKeyAPIToken); v != nil {
		return v.(string)
	}
	return ""
}

func WithAPIToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ContextKeyAPIToken, token)
}
