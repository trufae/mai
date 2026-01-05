package llm

import (
	"sort"
	"strings"
)

// ProviderInfo describes a supported provider and any accepted aliases.
type ProviderInfo struct {
	Name    string
	Aliases []string
}

var providerCatalog = []ProviderInfo{
	{Name: "ollama"},
	{Name: "lmstudio"},
	{Name: "openai"},
	{Name: "shimmy"},
	{Name: "openrouter"},
	{Name: "claude"},
	{Name: "gemini", Aliases: []string{"google"}},
	{Name: "mistral"},
	{Name: "deepseek"},
	{Name: "bedrock", Aliases: []string{"aws"}},
	{Name: "xai"},
	{Name: "ollamacloud"},
	{Name: "openapi"},
	{Name: "llamacli"},
}

var (
	providerAliasMap map[string]string
	providerNames    []string
)

func init() {
	providerAliasMap = make(map[string]string, len(providerCatalog))
	providerNames = make([]string, 0, len(providerCatalog))

	for _, info := range providerCatalog {
		canonical := strings.ToLower(info.Name)
		providerNames = append(providerNames, canonical)
		providerAliasMap[canonical] = canonical

		for _, alias := range info.Aliases {
			providerAliasMap[strings.ToLower(alias)] = canonical
		}
	}

	sort.Strings(providerNames)
}

// ProviderInfos returns a copy of the provider catalog.
func ProviderInfos() []ProviderInfo {
	out := make([]ProviderInfo, len(providerCatalog))
	copy(out, providerCatalog)
	return out
}

// CanonicalProviderName returns the canonical provider name for the given input.
func CanonicalProviderName(name string) (string, bool) {
	canonical, ok := providerAliasMap[strings.ToLower(name)]
	return canonical, ok
}

// IsValidProvider reports whether the given provider (or alias) is supported.
func IsValidProvider(name string) bool {
	_, ok := CanonicalProviderName(name)
	return ok
}

// GetValidProviders returns a set of all supported provider names and aliases.
func GetValidProviders() map[string]bool {
	result := make(map[string]bool, len(providerAliasMap))
	for name := range providerAliasMap {
		result[name] = true
	}
	return result
}

// GetValidProvidersList returns the sorted list of canonical provider names.
func GetValidProvidersList() []string {
	out := make([]string, len(providerNames))
	copy(out, providerNames)
	return out
}

// GetValidProvidersDisplay returns a user-facing string with providers and aliases.
func GetValidProvidersDisplay() string {
	parts := make([]string, 0, len(providerCatalog))
	for _, info := range providerCatalog {
		names := []string{strings.ToLower(info.Name)}
		for _, alias := range info.Aliases {
			names = append(names, strings.ToLower(alias))
		}
		parts = append(parts, strings.Join(names, "/"))
	}
	return strings.Join(parts, ", ")
}
