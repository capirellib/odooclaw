package agent

import (
	"context"
	"testing"

	"github.com/nicolasramos/odooclaw/pkg/bus"
	"github.com/nicolasramos/odooclaw/pkg/config"
	"github.com/nicolasramos/odooclaw/pkg/providers"
)

func TestAnnouncesWork(t *testing.T) {
	promises := []string{
		"Todo esto lo haré para julio a septiembre.\nAhora, procederé a consultar los registros del sistema Odoo. Esto puede tomar un momento.",
		"Voy a consultar las facturas de septiembre.",
		"Let me check the invoices.",
	}
	for _, text := range promises {
		if !announcesWork(text) {
			t.Errorf("%q announces work", text)
		}
	}
	answers := []string{
		"Septiembre: $ 25.808.197 con impuestos.",
		"Encontré 3 clientes llamados Acme. ¿Cuál querés que consulte?",
		"Voy a necesitar el período: ¿qué meses querés ver?",
		"",
	}
	for _, text := range answers {
		if announcesWork(text) {
			t.Errorf("%q is an answer, not a promise", text)
		}
	}
}

// promiseThenWorkProvider first only announces, then calls a tool when told
// to act, then answers with the result.
type promiseThenWorkProvider struct {
	calls  int
	nudged bool
}

func (p *promiseThenWorkProvider) GetDefaultModel() string { return "mock-model" }
func (p *promiseThenWorkProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.calls++
	last := messages[len(messages)-1]
	switch {
	case p.calls == 1:
		return &providers.LLMResponse{Content: "Ahora, procederé a consultar los registros. Esto puede tomar un momento."}, nil
	case last.Role == "user" && last.Content == actNowNudge:
		p.nudged = true
		return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "call-1", Name: "list_dir", Arguments: map[string]any{"path": "."}}}}, nil
	default:
		return &providers.LLMResponse{Content: "Listo: 3 usuarios usaron WhatsApp."}, nil
	}
}

func TestAnnounceOnlyReplyIsAskedToAct(t *testing.T) {
	cfg := &config.Config{Agents: config.AgentsConfig{Defaults: config.AgentDefaults{
		Workspace: t.TempDir(), Model: "test-model", MaxTokens: 4096, MaxToolIterations: 10,
	}}}
	provider := &promiseThenWorkProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	agent := al.registry.GetDefaultAgent()
	agent.Sessions.GetOrCreate("s")
	response, err := al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey: "s", Channel: "test", ChatID: "c", UserMessage: "¿Quién usó WhatsApp?", DefaultResponse: defaultResponse,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !provider.nudged || response != "Listo: 3 usuarios usaron WhatsApp." {
		t.Fatalf("nudged=%v response=%q", provider.nudged, response)
	}
}
