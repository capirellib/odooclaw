package metering

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nicolasramos/odooclaw/pkg/config"
	"github.com/nicolasramos/odooclaw/pkg/providers"
)

func TestAuthorizeCachesForResponseTTL(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get(VersionHeader) != Version {
			t.Errorf("version header = %q, want %q", r.Header.Get(VersionHeader), Version)
		}
		if r.Header.Get("X-Betta-Service-Token") != "service-secret" {
			t.Error("missing service token")
		}
		_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: true, TTL: 60})
	}))
	defer server.Close()
	client := newControlClient(server.URL, "", "service-secret", time.Second)
	for range 2 {
		if _, err := client.authorize(context.Background(), "bai-client"); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("authorize calls = %d, want 1", got)
	}
}

func TestUsageQueueRetriesAndDeletesOnlyAfterOK(t *testing.T) {
	var calls atomic.Int32
	delivered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, `{"ok":false}`, http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		select {
		case delivered <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	client := newControlClient(server.URL, "", "secret", time.Second)
	queue, err := newUsageQueue(filepath.Join(t.TempDir(), "usage.sqlite"), client)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.close()
	if err := queue.enqueue(UsageReport{Token: "bai-x", GenerationID: "gen-1", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		queue.deliverOne(context.Background())
		select {
		case <-delivered:
			goto done
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatal("usage was not delivered after retry")
done:
	for time.Now().Before(deadline) {
		var count int
		if err := queue.db.QueryRow(`SELECT count(*) FROM usage_queue`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("delivered usage remained in durable queue")
}

type fakeProvider struct {
	mu     sync.Mutex
	models []string
}

func (f *fakeProvider) GetDefaultModel() string { return "default" }
func (f *fakeProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	f.mu.Lock()
	f.models = append(f.models, model)
	call := len(f.models)
	f.mu.Unlock()
	content := "answer"
	if call == 1 {
		content = "2"
	}
	return &providers.LLMResponse{ID: "gen-test-" + model, Model: model, Content: content, Usage: &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12, Cost: 0.001}}, nil
}

func TestProviderUsesOdooRoutingAndQueuesBothCalls(t *testing.T) {
	var authorizeCalls atomic.Int32
	var usageCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/authorize":
			authorizeCalls.Add(1)
			_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: true, TTL: 45, Routing: RoutingConfig{ClassifierEnabled: true, ClassifierModel: "classifier", ClassifierTimeout: 1, ClassifierFallback: 2, FreeCascade: []string{"free"}, PaidCascade: []string{"paid"}, PremiumEnabled: true, AutoPremiumEnabled: true}})
		case "/api/v1/usage":
			usageCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	delegate := &fakeProvider{}
	wrapped, err := Wrap(config.MeteringConfig{Enabled: true, OdooURL: server.URL, ServiceToken: "secret", CustomerToken: "bai-client", QueuePath: filepath.Join(t.TempDir(), "queue.sqlite"), HTTPTimeoutSeconds: 1}, t.TempDir(), delegate)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.(*Provider).Close()
	ctx := WithRequest(context.Background(), "prepare a complex accounting report")
	if _, err := wrapped.Chat(ctx, []providers.Message{{Role: "user", Content: "hello"}}, nil, "requested", nil); err != nil {
		t.Fatal(err)
	}
	delegate.mu.Lock()
	models := append([]string(nil), delegate.models...)
	delegate.mu.Unlock()
	if len(models) != 2 || models[0] != "classifier" || models[1] != "paid" {
		t.Fatalf("models = %v", models)
	}
	deadline := time.Now().Add(3 * time.Second)
	for usageCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if usageCalls.Load() != 2 {
		t.Fatalf("usage calls = %d, want 2", usageCalls.Load())
	}
	if authorizeCalls.Load() != 1 {
		t.Fatalf("authorize calls = %d, want 1", authorizeCalls.Load())
	}
}

func TestParseComplexity(t *testing.T) {
	for input, want := range map[string]string{"1": "1", "complexity: 2": "2", "10": ""} {
		if got := parseComplexity(input); got != want {
			t.Errorf("parseComplexity(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRoutedModels_ExplicitPremiumSelectionMustBeInAllowedCascade(t *testing.T) {
	routing := RoutingConfig{
		FreeCascade:    []string{"openrouter/free"},
		PaidCascade:    []string{"openai/gpt-5", "anthropic/claude-sonnet"},
		AllowedModels:  []string{"openai/gpt-5"},
		PremiumEnabled: true,
	}

	if got := routedModels("default", "openai/gpt-5", "1", routing); len(got) != 1 || got[0] != "openai/gpt-5" {
		t.Fatalf("allowed explicit selection = %v, want [openai/gpt-5]", got)
	}
	if got := routedModels("default", "anthropic/claude-sonnet", "2", routing); len(got) != 0 {
		t.Fatalf("non-allowed explicit selection = %v, want none", got)
	}
	if got := routedModels("default", "attacker/arbitrary-model", "2", routing); len(got) != 0 {
		t.Fatalf("unknown explicit selection = %v, want none", got)
	}
}

func TestRoutedModels_UsesFreeUntilPremiumIsEnabled(t *testing.T) {
	routing := RoutingConfig{
		FreeCascade:    []string{"openrouter/free"},
		PaidCascade:    []string{"openai/gpt-5"},
		PremiumEnabled: false,
	}
	if got := routedModels("default", "", "2", routing); len(got) != 1 || got[0] != "openrouter/free" {
		t.Fatalf("automatic route without premium credit = %v, want [openrouter/free]", got)
	}
	if got := routedModels("default", "openai/gpt-5", "1", routing); len(got) != 0 {
		t.Fatalf("explicit premium without credit = %v, want none", got)
	}
}

func TestProviderExplainsPremiumCreditExhaustion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AuthorizeResponse{
			OK:  true,
			TTL: 1,
			Routing: RoutingConfig{
				FreeCascade:    []string{"openrouter/free"},
				PaidCascade:    []string{"openai/gpt-5"},
				PremiumEnabled: false,
			},
		})
	}))
	defer server.Close()
	wrapped, err := Wrap(config.MeteringConfig{Enabled: true, OdooURL: server.URL, ServiceToken: "secret", CustomerToken: "bai-client", QueuePath: filepath.Join(t.TempDir(), "queue.sqlite"), HTTPTimeoutSeconds: 1}, t.TempDir(), &fakeProvider{})
	if err != nil {
		t.Fatal(err)
	}
	defer wrapped.(*Provider).Close()
	ctx := WithModelSelection(WithRequest(context.Background(), "read this invoice"), "openai/gpt-5")
	resp, err := wrapped.Chat(ctx, []providers.Message{{Role: "user", Content: "read this invoice"}}, nil, "default", nil)
	if err != nil || !strings.Contains(resp.Content, "IA base sigue disponible") {
		t.Fatalf("premium exhaustion answer = %v / %v", resp, err)
	}
}

// tierServer serves a fixed routing and records every usage report.
func tierServer(t *testing.T, routing RoutingConfig) (*httptest.Server, *[]UsageReport, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var reports []UsageReport
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/authorize":
			_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: true, TTL: 1, Routing: routing})
		case "/api/v1/usage":
			var report UsageReport
			_ = json.NewDecoder(r.Body).Decode(&report)
			mu.Lock()
			reports = append(reports, report)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		}
	}))
	t.Cleanup(server.Close)
	return server, &reports, &mu
}

func waitReports(t *testing.T, reports *[]UsageReport, mu *sync.Mutex, want int) []UsageReport {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := append([]UsageReport(nil), (*reports)...)
		mu.Unlock()
		if len(got) >= want {
			return got
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("expected %d usage reports", want)
	return nil
}

// recordingProvider answers with the classifier verdict first, then text,
// and keeps the options each call received.
type recordingProvider struct {
	mu      sync.Mutex
	verdict string
	models  []string
	options []map[string]any
}

func (f *recordingProvider) GetDefaultModel() string { return "default" }
func (f *recordingProvider) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, options map[string]any) (*providers.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.models = append(f.models, model)
	f.options = append(f.options, options)
	content := "answer"
	if model == "classifier" {
		content = f.verdict
	}
	return &providers.LLMResponse{ID: fmt.Sprintf("gen-%s-%d", model, len(f.models)), Model: model, Content: content, Usage: &providers.UsageInfo{TotalTokens: 5, Cost: 0.0001}}, nil
}

func wrapFor(t *testing.T, url string, delegate providers.LLMProvider) *Provider {
	t.Helper()
	wrapped, err := Wrap(config.MeteringConfig{Enabled: true, OdooURL: url, ServiceToken: "secret", CustomerToken: "bai-client", QueuePath: filepath.Join(t.TempDir(), "queue.sqlite"), HTTPTimeoutSeconds: 1}, t.TempDir(), delegate)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wrapped.(*Provider).Close)
	return wrapped.(*Provider)
}

var baseRouting = RoutingConfig{
	ClassifierEnabled: true, ClassifierModel: "classifier", ClassifierTimeout: 1, ClassifierFallback: 1,
	FreeCascade: []string{"free"}, PaidCascade: []string{"paid"}, PremiumEnabled: false,
	BaseMaxTokens: 256, BaseRemaining: 0.5, PremiumNotice: "Pedí créditos a Betta.",
}

// Without credit, a complex question is answered by the base AI, capped,
// with the notice appended once to the final answer.
func TestComplexRequestWithoutCreditUsesBaseAndNotifies(t *testing.T) {
	server, reports, mu := tierServer(t, baseRouting)
	delegate := &recordingProvider{verdict: "2"}
	p := wrapFor(t, server.URL, delegate)
	ctx := WithRequest(context.Background(), "analyze the margins of every product line")
	resp, err := p.Chat(ctx, []providers.Message{{Role: "user", Content: "analyze"}}, nil, "default", map[string]any{"max_tokens": 8192})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(resp.Content, "Pedí créditos a Betta.") {
		t.Fatalf("answer without notice: %q", resp.Content)
	}
	again, _ := p.Chat(ctx, []providers.Message{{Role: "user", Content: "analyze"}}, nil, "default", nil)
	if strings.Contains(again.Content, "Pedí créditos") {
		t.Fatal("the notice must be shown once per user turn")
	}
	delegate.mu.Lock()
	if delegate.models[1] != "free" || delegate.options[1]["max_tokens"] != 256 {
		t.Fatalf("base call = %v %v", delegate.models, delegate.options[1])
	}
	delegate.mu.Unlock()
	for _, r := range waitReports(t, reports, mu, 3) {
		if r.Tier != "base" {
			t.Fatalf("report %+v must be base tier", r)
		}
	}
}

// Without credit, an image is not sent to any model.
func TestImageWithoutCreditIsNotProcessed(t *testing.T) {
	server, reports, mu := tierServer(t, baseRouting)
	delegate := &recordingProvider{verdict: "1"}
	p := wrapFor(t, server.URL, delegate)
	ctx := WithRequest(context.Background(), "read this receipt")
	resp, err := p.Chat(ctx, []providers.Message{{Role: "user", Content: "read this receipt", Media: []string{"data:image/png;base64,AAAA"}}}, nil, "default", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "Pedí créditos a Betta." {
		t.Fatalf("answer = %q", resp.Content)
	}
	if len(delegate.models) != 0 {
		t.Fatalf("no model should be called, got %v", delegate.models)
	}
	got := waitReports(t, reports, mu, 1)
	if got[0].Error != "premium_required" || got[0].HTTPStatus != 402 || got[0].Cost != 0 {
		t.Fatalf("report = %+v", got[0])
	}
}

// With credit, an image goes to the paid cascade even without auto premium.
func TestImageWithCreditUsesPremium(t *testing.T) {
	routing := baseRouting
	routing.PremiumEnabled = true
	server, reports, mu := tierServer(t, routing)
	delegate := &recordingProvider{verdict: "1"}
	p := wrapFor(t, server.URL, delegate)
	ctx := WithRequest(context.Background(), "read this receipt")
	if _, err := p.Chat(ctx, []providers.Message{{Role: "user", Content: "read", Media: []string{"x.png"}}}, nil, "default", nil); err != nil {
		t.Fatal(err)
	}
	if len(delegate.models) != 1 || delegate.models[0] != "paid" {
		t.Fatalf("models = %v", delegate.models)
	}
	if got := waitReports(t, reports, mu, 1); got[0].Tier != "premium" || got[0].ClassifierSource != "media" {
		t.Fatalf("report = %+v", got[0])
	}
}

func TestCapTokensKeepsSmallerLimitAndCopies(t *testing.T) {
	in := map[string]any{"max_tokens": 100}
	if out := capTokens(in, 256); out["max_tokens"] != 100 {
		t.Fatalf("smaller limit replaced: %v", out)
	}
	in = map[string]any{"max_tokens": 4096}
	out := capTokens(in, 256)
	if out["max_tokens"] != 256 || in["max_tokens"] != 4096 {
		t.Fatalf("cap = %v, original = %v", out, in)
	}
}

// A used-up allowance or credit is an answer for the customer, not an error:
// nothing is called and the reply is what Odoo said.
func TestLimitReachedIsAnsweredNotAnError(t *testing.T) {
	const msg = "Usaste los USD 2,00 de IA incluidos este mes. Se renuevan el 01/11."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: false, Error: "insufficient_credit", Message: msg, TTL: 1})
	}))
	defer server.Close()
	delegate := &recordingProvider{verdict: "1"}
	p := wrapFor(t, server.URL, delegate)
	resp, err := p.Chat(WithRequest(context.Background(), "hola"), []providers.Message{{Role: "user", Content: "hola"}}, nil, "default", nil)
	if err != nil || resp.Content != msg {
		t.Fatalf("answer = %v / %v", resp, err)
	}
	delegate.mu.Lock()
	defer delegate.mu.Unlock()
	if len(delegate.models) != 0 {
		t.Fatalf("no model may be called, got %v", delegate.models)
	}
}

// Any other refusal (a bad key, a misconfigured server) stays an error.
func TestUnknownRefusalStaysAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: false, Error: "invalid_api_key", TTL: 1})
	}))
	defer server.Close()
	p := wrapFor(t, server.URL, &recordingProvider{verdict: "1"})
	if _, err := p.Chat(WithRequest(context.Background(), "hola"), []providers.Message{{Role: "user", Content: "hola"}}, nil, "default", nil); err == nil {
		t.Fatal("an invalid key must stay an error")
	}
}

// From 80 % of the month's base AI, the answer says what is left, once.
func TestBaseNoticeIsAppendedOnce(t *testing.T) {
	routing := baseRouting
	routing.BaseNotice = "Te quedan USD 0,30 de los USD 2,00 de IA incluidos este mes."
	server, _, _ := tierServer(t, routing)
	p := wrapFor(t, server.URL, &recordingProvider{verdict: "1"})
	ctx := WithRequest(context.Background(), "buscá el cliente Acme")
	resp, err := p.Chat(ctx, []providers.Message{{Role: "user", Content: "buscá"}}, nil, "default", nil)
	if err != nil || !strings.HasSuffix(resp.Content, routing.BaseNotice) {
		t.Fatalf("answer = %v / %v", resp, err)
	}
	again, _ := p.Chat(ctx, []providers.Message{{Role: "user", Content: "buscá"}}, nil, "default", nil)
	if strings.Contains(again.Content, "Te quedan") {
		t.Fatal("the base notice must be shown once per user turn")
	}
}

// A pause set by the customer's administrator is answered, not an error.
func TestPausedAIIsAnsweredNotAnError(t *testing.T) {
	const msg = "La IA está pausada para tu empresa."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: false, Error: "ai_paused", Message: msg, TTL: 1})
	}))
	defer server.Close()
	p := wrapFor(t, server.URL, &recordingProvider{verdict: "1"})
	resp, err := p.Chat(WithRequest(context.Background(), "hola"), []providers.Message{{Role: "user", Content: "hola"}}, nil, "default", nil)
	if err != nil || resp.Content != msg {
		t.Fatalf("answer = %v / %v", resp, err)
	}
}

// Every usage report of a turn carries the Odoo user who asked.
func TestUsageReportsCarryTheOdooUser(t *testing.T) {
	users := make(chan int, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/authorize":
			_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: true, TTL: 45, Routing: RoutingConfig{FreeCascade: []string{"free"}}})
		case "/api/v1/usage":
			var report UsageReport
			_ = json.NewDecoder(r.Body).Decode(&report)
			users <- report.OdooUserID
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		}
	}))
	defer server.Close()
	p := wrapFor(t, server.URL, &recordingProvider{verdict: "1"})
	ctx := WithOdooUser(WithRequest(context.Background(), "hola"), "7")
	if _, err := p.Chat(ctx, []providers.Message{{Role: "user", Content: "hola"}}, nil, "default", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-users:
		if got != 7 {
			t.Fatalf("odoo_user_id = %d, want 7", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no usage report")
	}
}

// answersByModel replies with a fixed text per model.
type answersByModel struct {
	mu      sync.Mutex
	answers map[string]string
	models  []string
}

func (f *answersByModel) GetDefaultModel() string { return "default" }
func (f *answersByModel) Chat(_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any) (*providers.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.models = append(f.models, model)
	return &providers.LLMResponse{ID: "gen-" + model, Model: model, Content: f.answers[model], Usage: &providers.UsageInfo{PromptTokens: 1, CompletionTokens: 1, TotalTokens: 2}}, nil
}

func cascadeServer(t *testing.T, cascade []string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/authorize" {
			_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: true, TTL: 45, Routing: RoutingConfig{FreeCascade: cascade, ClassifierFallback: 1}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	t.Cleanup(server.Close)
	return server.URL
}

var odooTools = []providers.ToolDefinition{{Type: "function", Function: providers.ToolFunctionDefinition{Name: "odoo_get_totals"}}}

// A tool call written as text consulted nothing: the next model answers.
func TestToolCallWrittenAsTextTriesTheNextModel(t *testing.T) {
	delegate := &answersByModel{answers: map[string]string{
		"weak":   "Te doy el total.\n<function>mcp_odoo-manager_odoo_find_pending_invoices</function>",
		"strong": "Septiembre: $ 25.808.197",
	}}
	p := wrapFor(t, cascadeServer(t, []string{"weak", "strong"}), delegate)
	resp, err := p.Chat(WithRequest(context.Background(), "total de septiembre"), []providers.Message{{Role: "user", Content: "total de septiembre"}}, odooTools, "default", nil)
	if err != nil || resp.Content != "Septiembre: $ 25.808.197" {
		t.Fatalf("answer = %v / %v", resp, err)
	}
}

// When every model writes it as text, the customer reads a plain notice.
func TestEveryModelWritingTheCallGivesANotice(t *testing.T) {
	delegate := &answersByModel{answers: map[string]string{
		"weak": `{"name": "odoo_get_totals", "arguments": {}}`,
	}}
	p := wrapFor(t, cascadeServer(t, []string{"weak"}), delegate)
	resp, err := p.Chat(WithRequest(context.Background(), "total"), []providers.Message{{Role: "user", Content: "total"}}, odooTools, "default", nil)
	if err != nil || resp.Content != toolCallFailedNotice {
		t.Fatalf("answer = %v / %v", resp, err)
	}
}

func TestPlainAnswersAreNotToolCalls(t *testing.T) {
	for _, text := range []string{"Septiembre: $ 25.808.197", "La función de facturas está en Contabilidad."} {
		if toolCallAsText(text, odooTools) {
			t.Fatalf("%q is not a tool call", text)
		}
	}
}
