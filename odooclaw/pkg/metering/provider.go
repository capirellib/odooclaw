package metering

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nicolasramos/odooclaw/pkg/config"
	"github.com/nicolasramos/odooclaw/pkg/providers"
)

type Provider struct {
	delegate providers.LLMProvider
	client   *controlClient
	queue    *usageQueue
	token    string
}

func Wrap(cfg config.MeteringConfig, workspace string, delegate providers.LLMProvider) (providers.LLMProvider, error) {
	if !cfg.Enabled {
		return delegate, nil
	}
	if strings.TrimSpace(cfg.OdooURL) == "" || strings.TrimSpace(cfg.ServiceToken) == "" || strings.TrimSpace(cfg.CustomerToken) == "" {
		return nil, errors.New("metering requires odoo_url, service_token and customer_token")
	}
	timeout := time.Duration(cfg.HTTPTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	client := newControlClient(cfg.OdooURL, cfg.Database, cfg.ServiceToken, timeout)
	queuePath := strings.TrimSpace(cfg.QueuePath)
	if queuePath == "" {
		queuePath = filepath.Join(workspace, "metering", "usage.sqlite")
	}
	queue, err := newUsageQueue(queuePath, client)
	if err != nil {
		return nil, fmt.Errorf("open durable usage queue: %w", err)
	}
	return &Provider{delegate: delegate, client: client, queue: queue, token: cfg.CustomerToken}, nil
}

func (p *Provider) GetDefaultModel() string { return p.delegate.GetDefaultModel() }

func (p *Provider) Close() {
	p.queue.close()
	if stateful, ok := p.delegate.(providers.StatefulProvider); ok {
		stateful.Close()
	}
}

func (p *Provider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, requestedModel string, options map[string]any) (*providers.LLMResponse, error) {
	state := stateFromContext(ctx)
	if state == nil {
		state = &requestState{prompt: lastUserMessage(messages)}
	}
	state.once.Do(func() {
		state.auth, state.err = p.client.authorize(ctx, p.token)
		if state.err != nil {
			return
		}
		if !state.auth.OK {
			message := state.auth.Message
			if message == "" {
				message = state.auth.Error
			}
			state.err = fmt.Errorf("authorization denied: %s", message)
			return
		}
		// An image or a document needs a vision/OCR model: an advanced
		// feature, whatever the classifier would say about the text.
		if hasMedia(messages) {
			state.complexity, state.source = "2", "media"
			return
		}
		state.complexity, state.source, state.err = p.classify(ctx, state.prompt, state.auth.Routing)
	})
	if state.err != nil {
		return nil, state.err
	}
	routing := state.auth.Routing
	explicit := strings.TrimSpace(state.modelSelection) != ""

	// Advanced features without credit: an explicit premium model or an
	// image/document cannot be served by the base AI, so nothing is called
	// and the customer is told how to get credit. Odoo logs the attempt and
	// alerts the reseller's sales side.
	if !routing.PremiumEnabled && (explicit || state.source == "media") {
		if err := p.enqueueRequired(requestedModel, state); err != nil {
			return nil, err
		}
		return &providers.LLMResponse{Content: premiumNotice(routing), FinishReason: "stop"}, nil
	}

	models := routedModels(requestedModel, state.modelSelection, state.complexity, routing)
	if state.source == "media" && routing.PremiumEnabled && !explicit {
		models = routedModels(requestedModel, "", "2", RoutingConfig{
			FreeCascade: routing.FreeCascade, PaidCascade: routing.PaidCascade,
			PremiumEnabled: true, AutoPremiumEnabled: true, AllowedModels: routing.AllowedModels,
		})
	}
	tier := tierOf(models, routing)
	if tier == "base" {
		options = capTokens(options, routing.BaseMaxTokens)
	}
	var lastErr error
	for _, model := range models {
		response, err := p.delegate.Chat(ctx, messages, tools, model, options)
		if err != nil {
			lastErr = err
			continue
		}
		if err := p.enqueueResponse(response, requestedModel, model, state.complexity, state.source, tier); err != nil {
			return nil, fmt.Errorf("persist usage before returning provider response: %w", err)
		}
		// A complex request answered by the base AI: say once, at the end of
		// the final answer, that more is available with credit.
		if tier == "base" && state.complexity == "2" && !routing.PremiumEnabled &&
			len(response.ToolCalls) == 0 && !state.noticeShown {
			state.noticeShown = true
			response.Content = strings.TrimRight(response.Content, "\n") + "\n\n---\n" + premiumNotice(routing)
		}
		return response, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no model available after applying routing policy")
	}
	return nil, lastErr
}

// defaultPremiumNotice is used when Odoo sends no text of its own.
const defaultPremiumNotice = "Esta consulta necesita funciones avanzadas de IA (consultas complejas, imágenes u OCR). " +
	"La IA base sigue disponible para consultas simples. Para ampliar tu uso, comunicate con tu proveedor para obtener créditos."

func premiumNotice(routing RoutingConfig) string {
	if notice := strings.TrimSpace(routing.PremiumNotice); notice != "" {
		return notice
	}
	return defaultPremiumNotice
}

// hasMedia reports whether the current user turn carries an image or file.
func hasMedia(messages []providers.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return len(messages[i].Media) > 0
		}
	}
	return false
}

// tierOf tells whether the chosen models are the paid cascade (premium) or
// the economic one (base). It is what Odoo uses to let the monthly base
// allowance pay first.
func tierOf(models []string, routing RoutingConfig) string {
	paid := make(map[string]bool, len(routing.PaidCascade))
	for _, model := range routing.PaidCascade {
		paid[strings.TrimSpace(model)] = true
	}
	for _, model := range models {
		if paid[model] {
			return "premium"
		}
	}
	return "base"
}

// capTokens limits the answer of a base request. It copies options so the
// caller's map, shared across the tool loop, is never changed.
func capTokens(options map[string]any, limit int) map[string]any {
	if limit <= 0 {
		return options
	}
	out := make(map[string]any, len(options)+1)
	for k, v := range options {
		out[k] = v
	}
	if current, ok := out["max_tokens"].(int); ok && current > 0 && current <= limit {
		return out
	}
	out["max_tokens"] = limit
	return out
}

// enqueueRequired reports a request stopped for lack of credit. Nothing was
// generated, so it costs nothing; Odoo records it and alerts sales.
func (p *Provider) enqueueRequired(requestedModel string, state *requestState) error {
	what := strings.TrimSpace(state.modelSelection)
	if what == "" {
		what = "imagen o documento adjunto"
	}
	if requestedModel == "" {
		requestedModel = what
	}
	return p.queue.enqueue(UsageReport{
		Token: p.token, GenerationID: "required-" + uuid.NewString(), ModelRequested: what,
		Model: requestedModel, Complexity: state.complexity, ClassifierSource: state.source,
		HTTPStatus: 402, Error: "premium_required",
	})
}

func (p *Provider) classify(ctx context.Context, prompt string, routing RoutingConfig) (string, string, error) {
	fallback := fmt.Sprintf("%d", routing.ClassifierFallback)
	if fallback != "1" && fallback != "2" {
		fallback = "2"
	}
	if !routing.ClassifierEnabled {
		return fallback, "disabled", nil
	}
	if strings.TrimSpace(routing.ClassifierModel) == "" {
		return fallback, "fallback", nil
	}
	timeout := time.Duration(routing.ClassifierTimeout) * time.Second
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	classifierCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	classificationPrompt := "Classify the user request complexity. Reply with only 1 for simple/low-cost requests or 2 for complex requests requiring stronger reasoning. User request:\n" + prompt
	resp, err := p.delegate.Chat(classifierCtx, []providers.Message{{Role: "user", Content: classificationPrompt}}, nil, routing.ClassifierModel, map[string]any{"max_tokens": 4, "temperature": 0})
	if err != nil {
		return fallback, "fallback", nil
	}
	complexity := parseComplexity(resp.Content)
	if complexity == "" {
		complexity = fallback
		if err := p.enqueueResponse(resp, routing.ClassifierModel, routing.ClassifierModel, complexity, "fallback", "base"); err != nil {
			return "", "", fmt.Errorf("persist classifier usage: %w", err)
		}
		return complexity, "fallback", nil
	}
	if err := p.enqueueResponse(resp, routing.ClassifierModel, routing.ClassifierModel, complexity, "llm", "base"); err != nil {
		return "", "", fmt.Errorf("persist classifier usage: %w", err)
	}
	return complexity, "llm", nil
}

var complexityPattern = regexp.MustCompile(`(?:^|[^0-9])([12])(?:[^0-9]|$)`)

func parseComplexity(value string) string {
	match := complexityPattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

// routedModels selects the automatic cascade unless Odoo explicitly requested
// a model in the customer's paid cascade. A request is never sufficient on its
// own: it must also comply with the allow-list returned by the control plane.
func routedModels(requested, selected, complexity string, routing RoutingConfig) []string {
	selected = strings.TrimSpace(selected)
	if selected != "" {
		paid := make(map[string]bool, len(routing.PaidCascade))
		for _, model := range routing.PaidCascade {
			paid[strings.TrimSpace(model)] = true
		}
		allowed := make(map[string]bool, len(routing.AllowedModels))
		for _, model := range routing.AllowedModels {
			allowed[strings.TrimSpace(model)] = true
		}
		if routing.PremiumEnabled && paid[selected] && (len(allowed) == 0 || allowed[selected]) {
			return []string{selected}
		}
		return nil
	}

	models := routing.FreeCascade
	if complexity == "2" && routing.PremiumEnabled && routing.AutoPremiumEnabled {
		models = routing.PaidCascade
	}
	allowed := make(map[string]bool, len(routing.AllowedModels))
	for _, model := range routing.AllowedModels {
		allowed[model] = true
	}
	result := make([]string, 0, len(models)+1)
	seen := map[string]bool{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] || (len(allowed) > 0 && !allowed[model]) {
			continue
		}
		seen[model] = true
		result = append(result, model)
	}
	if len(result) == 0 && requested != "" && (len(allowed) == 0 || allowed[requested]) {
		result = append(result, requested)
	}
	return result
}

func (p *Provider) enqueueResponse(resp *providers.LLMResponse, requestedModel, fallbackModel, complexity, source, tier string) error {
	if resp == nil || resp.Usage == nil {
		return errors.New("provider response did not include usage")
	}
	id := strings.TrimSpace(resp.ID)
	if id == "" {
		id = "gen-" + uuid.NewString()
	}
	model := strings.TrimSpace(resp.Model)
	if model == "" {
		model = fallbackModel
	}
	return p.queue.enqueue(UsageReport{
		Token: p.token, GenerationID: id, ModelRequested: requestedModel, Model: model,
		PromptTokens: resp.Usage.PromptTokens, CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens: resp.Usage.TotalTokens, Cost: resp.Usage.Cost,
		Complexity: complexity, ClassifierSource: source, Tier: tier, HTTPStatus: 200,
	})
}

func lastUserMessage(messages []providers.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[i].Content
		}
	}
	return ""
}
