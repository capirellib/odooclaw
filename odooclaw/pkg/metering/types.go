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
	PremiumEnabled     bool     `json:"premium_enabled"`
	AutoPremiumEnabled bool     `json:"auto_premium_enabled"`
	AllowedModels      []string `json:"allowed_models"`
	// Base AI: what a customer without credit keeps. Odoo sends the answer
	// cap, the allowance left this month and the text that tells the
	// customer how to get credit.
	BaseMaxTokens int     `json:"base_max_tokens,omitempty"`
	BaseRemaining float64 `json:"base_remaining,omitempty"`
	PremiumNotice string  `json:"premium_notice,omitempty"`
	// BaseNotice tells a customer running on the monthly base AI how much
	// is left, once 80 % of it is used. Empty otherwise.
	BaseNotice string `json:"base_notice,omitempty"`
}

type AuthorizeResponse struct {
	OK             bool          `json:"ok"`
	Error          string        `json:"error,omitempty"`
	Message        string        `json:"message,omitempty"`
	PartnerID      int           `json:"partner_id,omitempty"`
	PartnerName    string        `json:"partner_name,omitempty"`
	Balance        float64       `json:"balance,omitempty"`
	Markup         float64       `json:"markup,omitempty"`
	PremiumEnabled bool          `json:"premium_enabled"`
	TTL            int           `json:"ttl,omitempty"`
	Routing        RoutingConfig `json:"routing,omitempty"`
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
	Tier             string  `json:"tier,omitempty"` // "base" or "premium"
	ClassifierSource string  `json:"classifier_source,omitempty"`
	HTTPStatus       int     `json:"http_status,omitempty"`
	Error            string  `json:"error,omitempty"`
}

type requestState struct {
	prompt         string
	modelSelection string
	once           sync.Once
	auth           AuthorizeResponse
	complexity     string
	source         string
	err            error
	// noticeShown keeps the "needs credit" notice to one per user turn: a
	// tool loop calls Chat several times for the same request.
	noticeShown bool
	// denied is what Odoo said when it refused the request (credit or base
	// AI used up, daily cap, blocked key): the customer reads it as the
	// answer, not as an error.
	denied string
	// baseNoticeShown keeps the "base AI left" line to one per user turn.
	baseNoticeShown bool
}

type requestKey struct{}

// WithRequest scopes one authorization and one complexity decision to a user
// turn. All tool-loop provider calls reuse that decision.
func WithRequest(ctx context.Context, prompt string) context.Context {
	return context.WithValue(ctx, requestKey{}, &requestState{prompt: prompt})
}

// WithModelSelection attaches an optional Odoo-requested premium model code
// to the current user turn. Provider-side policy validation is deliberately
// deferred until authorization returns the customer's allowed cascade.
func WithModelSelection(ctx context.Context, model string) context.Context {
	state := stateFromContext(ctx)
	if state == nil {
		state = &requestState{}
		ctx = context.WithValue(ctx, requestKey{}, state)
	}
	state.modelSelection = model
	return ctx
}

func stateFromContext(ctx context.Context) *requestState {
	state, _ := ctx.Value(requestKey{}).(*requestState)
	return state
}
