package metering

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
			_ = json.NewEncoder(w).Encode(AuthorizeResponse{OK: true, TTL: 45, Routing: RoutingConfig{ClassifierEnabled: true, ClassifierModel: "classifier", ClassifierTimeout: 1, ClassifierFallback: 2, FreeCascade: []string{"free"}, PaidCascade: []string{"paid"}}})
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
