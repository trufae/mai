package mcplib

import "context"

type contextKey string

const (
	ContextKeyAPIToken contextKey = "api_token"
)

func (r *AuthResult) Apply(ctx context.Context) context.Context {
	if r == nil {
		return ctx
	}
	if r.APIToken != "" {
		ctx = WithAPIToken(ctx, r.APIToken)
	}
	for _, kv := range r.ContextValues {
		ctx = context.WithValue(ctx, kv.Key, kv.Value)
	}
	return ctx
}

func GetAPIToken(ctx context.Context) string {
	if v := ctx.Value(ContextKeyAPIToken); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func WithAPIToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, ContextKeyAPIToken, token)
}
