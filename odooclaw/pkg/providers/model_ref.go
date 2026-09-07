package providers

import "strings"

// ModelRef represents a parsed model reference with provider and model name.
type ModelRef struct {
	Provider string
	Model    string
}

// ParseModelRef parses "anthropic/claude-opus" into {Provider: "anthropic", Model: "claude-opus"}.
// If no slash present, uses defaultProvider.
// Returns nil for empty input.
func ParseModelRef(raw string, defaultProvider string) *ModelRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if idx := strings.Index(raw, "/"); idx > 0 {
		provider := NormalizeProvider(raw[:idx])
		model := strings.TrimSpace(raw[idx+1:])
		if model == "" {
			return nil
		}
		// OpenRouter model IDs contain their own namespace (for example,
		// "openrouter/free" or "google/gemini-2.5-flash"). Keep the full
		// value so the OpenAI-compatible request uses the ID expected by
		// OpenRouter, including when this candidate is a fallback.
		if provider == "openrouter" {
			model = raw
		}
		return &ModelRef{Provider: provider, Model: model}
	}

	return &ModelRef{
		Provider: NormalizeProvider(defaultProvider),
		Model:    raw,
	}
}

// NormalizeProvider normalizes provider identifiers to canonical form.
func NormalizeProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))

	switch p {
	case "z.ai", "z-ai":
		return "zai"
	case "opencode-zen":
		return "opencode"
	case "qwen":
		return "qwen-portal"
	case "kimi-code":
		return "kimi-coding"
	case "gpt":
		return "openai"
	case "claude":
		return "anthropic"
	case "glm":
		return "zhipu"
	case "google":
		return "gemini"
	}

	return p
}

// ModelKey returns a canonical "provider/model" key for deduplication.
func ModelKey(provider, model string) string {
	return NormalizeProvider(provider) + "/" + strings.ToLower(strings.TrimSpace(model))
}
