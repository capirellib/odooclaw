package metering

import (
	"context"
	"sync"
)

type RoutingConfig struct {
	ClassifierEnabled  bool     `json:"classifier_enabled"`
	ClassifierModel    string   `json:"classifier_model"`
	ClassifierTimeout  int      `json:"classifier_timeout"`
	ClassifierFallback int      `json:"classifier_fallback"`
	FreeCascade        []string `json:"free_cascade"`
	PaidCascade        []string `json:"paid_cascade"`
	AllowedModels      []string `json:"allowed_models"`
}

type AuthorizeResponse struct {
	OK          bool          `json:"ok"`
	Error       string        `json:"error,omitempty"`
	Message     string        `json:"message,omitempty"`
	PartnerID   int           `json:"partner_id,omitempty"`
	PartnerName string        `json:"partner_name,omitempty"`
	Balance     float64       `json:"balance,omitempty"`
	Markup      float64       `json:"markup,omitempty"`
	TTL         int           `json:"ttl,omitempty"`
	Routing     RoutingConfig `json:"routing,omitempty"`
}

type UsageReport struct {
	Token            string  `json:"token"`
	RequestID        string  `json:"request_id,omitempty"`
	GenerationID     string  `json:"generation_id"`
	ModelRequested   string  `json:"model_requested,omitempty"`
	Model            string  `json:"model"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens,omitempty"`
	Cost             float64 `json:"cost"`
	Complexity       string  `json:"complexity,omitempty"`
	ClassifierSource string  `json:"classifier_source,omitempty"`
	HTTPStatus       int     `json:"http_status,omitempty"`
	Error            string  `json:"error,omitempty"`
}

type requestState struct {
	prompt     string
	once       sync.Once
	auth       AuthorizeResponse
	complexity string
	source     string
	err        error
}

type requestKey struct{}

// WithRequest scopes one authorization and one complexity decision to a user
// turn. All tool-loop provider calls reuse that decision.
func WithRequest(ctx context.Context, prompt string) context.Context {
	return context.WithValue(ctx, requestKey{}, &requestState{prompt: prompt})
}

func stateFromContext(ctx context.Context) *requestState {
	state, _ := ctx.Value(requestKey{}).(*requestState)
	return state
}
