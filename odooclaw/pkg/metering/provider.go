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
		state.complexity, state.source, state.err = p.classify(ctx, state.prompt, state.auth.Routing)
	})
	if state.err != nil {
		return nil, state.err
	}

	models := routedModels(requestedModel, state.complexity, state.auth.Routing)
	var lastErr error
	for _, model := range models {
		response, err := p.delegate.Chat(ctx, messages, tools, model, options)
		if err != nil {
			lastErr = err
			continue
		}
		if err := p.enqueueResponse(response, requestedModel, model, state.complexity, state.source); err != nil {
			return nil, fmt.Errorf("persist usage before returning provider response: %w", err)
		}
		return response, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no model available after applying routing policy")
	}
	return nil, lastErr
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
		if err := p.enqueueResponse(resp, routing.ClassifierModel, routing.ClassifierModel, complexity, "fallback"); err != nil {
			return "", "", fmt.Errorf("persist classifier usage: %w", err)
		}
		return complexity, "fallback", nil
	}
	if err := p.enqueueResponse(resp, routing.ClassifierModel, routing.ClassifierModel, complexity, "llm"); err != nil {
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

func routedModels(requested, complexity string, routing RoutingConfig) []string {
	models := routing.FreeCascade
	if complexity == "2" {
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

func (p *Provider) enqueueResponse(resp *providers.LLMResponse, requestedModel, fallbackModel, complexity, source string) error {
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
		Complexity: complexity, ClassifierSource: source, HTTPStatus: 200,
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
