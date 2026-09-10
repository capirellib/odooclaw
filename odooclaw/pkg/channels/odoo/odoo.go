package odoo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nicolasramos/odooclaw/pkg/bus"
	"github.com/nicolasramos/odooclaw/pkg/channels"
	"github.com/nicolasramos/odooclaw/pkg/config"
	"github.com/nicolasramos/odooclaw/pkg/utils"
)

type OdooChannel struct {
	*channels.BaseChannel
	config config.OdooConfig
	client *http.Client
}

type OdooWebhookPayload struct {
	MessageID         int    `json:"message_id"`
	Model             string `json:"model"`
	ResID             int    `json:"res_id"`
	ReplyModel        string `json:"reply_model"`
	ReplyResID        int    `json:"reply_res_id"`
	AuthorID          int    `json:"author_id"`
	AuthorUserID      int    `json:"author_user_id"`
	AuthorName        string `json:"author_name"`
	Body              string `json:"body"`
	IsDM              bool   `json:"is_dm"`
	CompanyID         int    `json:"company_id"`
	AllowedCompanyIDs []int  `json:"allowed_company_ids"`
	// ModelAlias is an optional premium model code selected by Odoo. It is
	// treated as an untrusted request: the metering policy validates it against
	// the customer's allowed premium cascade before it reaches a provider.
	ModelAlias string `json:"model_alias,omitempty"`
}

type OdooReplyPayload struct {
	Model   string `json:"model"`
	ResID   int    `json:"res_id"`
	Message string `json:"message"`
}

func NewOdooChannel(cfg config.OdooConfig, messageBus *bus.MessageBus) (*OdooChannel, error) {
	base := channels.NewBaseChannel("odoo", cfg, messageBus, cfg.AllowFrom,
		channels.WithReasoningChannelID(cfg.ReasoningChannelID),
	)

	ch := &OdooChannel{
		BaseChannel: base,
		config:      cfg,
		client:      &http.Client{Timeout: 10 * time.Second},
	}

	base.SetOwner(ch)
	return ch, nil
}

func (c *OdooChannel) Start(ctx context.Context) error {
	c.SetRunning(true)
	slog.Info("Odoo channel started (Webhook Mode)")
	return nil
}

func (c *OdooChannel) Stop(ctx context.Context) error {
	c.SetRunning(false)
	return nil
}

func (c *OdooChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	parts := strings.Split(msg.ChatID, "_")
	if len(parts) != 2 {
		return fmt.Errorf("invalid odoo chatID format: %s", msg.ChatID)
	}

	modelName := parts[0]
	resID, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("invalid res_id in chatID: %s", parts[1])
	}

	odooURL := os.Getenv("ODOO_URL")
	if odooURL == "" {
		slog.Warn("ODOO_URL env var not set, cannot send message back to Odoo")
		return nil
	}

	reply := OdooReplyPayload{
		Model:   modelName,
		ResID:   resID,
		Message: utils.RemoveReasoning(msg.Content),
	}

	jsonData, err := json.Marshal(reply)
	if err != nil {
		return err
	}

	endpoint := buildReplyEndpoint(odooURL, c.config.TargetDB, os.Getenv("ODOO_DB"))
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		slog.Error("Failed to send message to Odoo", "error", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Error("Odoo returned non-200 status", "status", resp.StatusCode)
		return fmt.Errorf("odoo api error: %d", resp.StatusCode)
	}

	slog.Info("Message sent to Odoo successfully", "chatID", msg.ChatID)
	return nil
}

func buildReplyEndpoint(odooURL, targetDB, fallbackEnvDB string) string {
	endpoint := fmt.Sprintf("%s/odooclaw/reply", strings.TrimSuffix(odooURL, "/"))

	resolvedDB := strings.TrimSpace(targetDB)
	if resolvedDB == "" {
		resolvedDB = strings.TrimSpace(fallbackEnvDB)
	}

	if resolvedDB != "" {
		endpoint = fmt.Sprintf("%s?db=%s", endpoint, resolvedDB)
	}

	return endpoint
}

// openRouterBase returns the provider base URL, defaulting to the public API.
func (c *OdooChannel) openRouterBase() string {
	base := strings.TrimSuffix(strings.TrimSpace(os.Getenv("ODOOCLAW_PROVIDERS_OPENROUTER_API_BASE")), "/")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	return base
}

// fetchModelCatalog returns the provider catalog normalised for Odoo, so the
// Odoo module never has to reach openrouter.ai nor know its response schema.
func (c *OdooChannel) fetchModelCatalog(ctx context.Context) ([]map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.openRouterBase()+"/models", nil)
	if err != nil {
		return nil, err
	}
	// The catalog is public; the key is only sent when present so that
	// per-account visibility is honoured.
	if key := strings.TrimSpace(os.Getenv("ODOOCLAW_PROVIDERS_OPENROUTER_API_KEY")); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			Description   string `json:"description"`
			ContextLength int    `json:"context_length"`
			Architecture  struct {
				Modality string `json:"modality"`
			} `json:"architecture"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}

	precio := func(v string) float64 {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		return f
	}

	models := make([]map[string]any, 0, len(payload.Data))
	for _, m := range payload.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		entrada := precio(m.Pricing.Prompt)
		salida := precio(m.Pricing.Completion)
		nombre := m.Name
		if strings.TrimSpace(nombre) == "" {
			nombre = m.ID
		}
		if len(m.Description) > 2000 {
			m.Description = m.Description[:2000]
		}
		models = append(models, map[string]any{
			"code":             m.ID,
			"name":             nombre,
			"description":      m.Description,
			"context_length":   m.ContextLength,
			"modality":         m.Architecture.Modality,
			"prompt_price":     entrada,
			"completion_price": salida,
			"free":             entrada == 0 && salida == 0,
		})
	}
	return models, nil
}

// handleConfig answers everything the Odoo module needs to operate without
// holding provider credentials or contacting any third party itself.
func (c *OdooChannel) handleConfig(w http.ResponseWriter, r *http.Request) {
	respuesta := map[string]any{
		"provider":       "openrouter",
		"target_db":      c.config.TargetDB,
		"webhook_path":   c.WebhookPath(),
		"token_required": c.config.WebhookToken != "",
	}

	if models, err := c.fetchModelCatalog(r.Context()); err != nil {
		slog.Warn("Could not fetch provider catalog", "error", err)
		respuesta["models_error"] = err.Error()
		respuesta["models"] = []map[string]any{}
	} else {
		respuesta["models"] = models
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(respuesta)
}

func (c *OdooChannel) WebhookPath() string {
	if c.config.WebhookPath != "" {
		return c.config.WebhookPath
	}
	return "/webhook/odoo"
}

func (c *OdooChannel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Odoo queries provider state through this same private channel webhook so
	// that it never needs the provider credentials itself. No secret is ever
	// returned: only derived, non-sensitive data.
	if r.Method == http.MethodGet {
		if action := r.URL.Query().Get("action"); action != "" {
			if c.config.WebhookToken != "" && r.Header.Get("X-OdooClaw-Token") != c.config.WebhookToken {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			switch action {
			case "credit_status":
				c.handleCreditStatus(w, r)
			case "config":
				c.handleConfig(w, r)
			default:
				http.Error(w, "Unknown action", http.StatusNotFound)
			}
			return
		}
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Verify webhook token if configured
	token := r.Header.Get("X-OdooClaw-Token")
	if c.config.WebhookToken != "" && token != c.config.WebhookToken {
		slog.Warn("Rejected webhook: invalid token", "remote", r.RemoteAddr)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload OdooWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("Failed to parse Odoo webhook", "error", err)
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if !payload.IsDM && !c.config.AllowGroupMentions {
		slog.Info("Ignoring Odoo group mention because allow_group_mentions is disabled", "model", payload.Model, "res_id", payload.ResID)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ignored","reason":"group_mentions_disabled"}`))
		return
	}

	sourceChatID := fmt.Sprintf("%s_%d", payload.Model, payload.ResID)
	replyModel := strings.TrimSpace(payload.ReplyModel)
	if replyModel == "" {
		replyModel = payload.Model
	}
	replyResID := payload.ReplyResID
	if replyResID <= 0 {
		replyResID = payload.ResID
	}
	replyChatID := fmt.Sprintf("%s_%d", replyModel, replyResID)
	hasPrivateReplyTarget := !payload.IsDM && payload.ReplyModel != "" && payload.ReplyResID > 0

	senderNumericID := payload.AuthorUserID
	if senderNumericID <= 0 {
		senderNumericID = payload.AuthorID
	}
	senderID := fmt.Sprintf("%d", senderNumericID)

	sender := bus.SenderInfo{
		Platform:    "odoo",
		PlatformID:  senderID,
		Username:    payload.AuthorName,
		DisplayName: payload.AuthorName,
	}

	peerKind := "group"
	peerID := sourceChatID
	if payload.IsDM || hasPrivateReplyTarget {
		peerKind = "direct"
		peerID = senderID
	}

	peer := bus.Peer{
		Kind: peerKind,
		ID:   peerID,
	}

	content := strings.TrimSpace(payload.Body)

	// Enrich message with record context when coming from a non-channel model
	if payload.Model != "" && payload.Model != "discuss.channel" && payload.ResID > 0 {
		content = fmt.Sprintf(
			"[Odoo Context: %s ID=%d]\n%s",
			payload.Model,
			payload.ResID,
			content,
		)
	}

	// Odoo filters mentions server-side before sending to the webhook.
	var mediaPaths []string
	metadata := map[string]string{
		"model":        payload.Model,
		"res_id":       strconv.Itoa(payload.ResID),
		"reply_model":  replyModel,
		"reply_res_id": strconv.Itoa(replyResID),
	}
	if modelAlias := strings.TrimSpace(payload.ModelAlias); modelAlias != "" {
		metadata["model_alias"] = modelAlias
	}
	if payload.CompanyID > 0 {
		metadata["company_id"] = strconv.Itoa(payload.CompanyID)
	}
	if len(payload.AllowedCompanyIDs) > 0 {
		if b, err := json.Marshal(payload.AllowedCompanyIDs); err == nil {
			metadata["allowed_company_ids"] = string(b)
		}
	}

	c.HandleMessage(r.Context(), peer, strconv.Itoa(payload.MessageID), senderID, replyChatID, content, mediaPaths, metadata, sender)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (c *OdooChannel) handleCreditStatus(w http.ResponseWriter, r *http.Request) {
	apiKey := strings.TrimSpace(os.Getenv("ODOOCLAW_PROVIDERS_OPENROUTER_API_KEY"))
	if apiKey == "" {
		http.Error(w, "OpenRouter API key is not configured through the environment", http.StatusServiceUnavailable)
		return
	}
	apiBase := strings.TrimSuffix(strings.TrimSpace(os.Getenv("ODOOCLAW_PROVIDERS_OPENROUTER_API_BASE")), "/")
	if apiBase == "" {
		apiBase = "https://openrouter.ai/api/v1"
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, apiBase+"/key", nil)
	if err != nil {
		http.Error(w, "Unable to create OpenRouter request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		http.Error(w, "Unable to contact OpenRouter", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "OpenRouter rejected the credit query", http.StatusBadGateway)
		return
	}
	var payload struct {
		Data struct {
			LimitRemaining float64 `json:"limit_remaining"`
			Limit          float64 `json:"limit"`
			LimitReset     string  `json:"limit_reset"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid OpenRouter response", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"balance": payload.Data.LimitRemaining, "limit": payload.Data.Limit, "reset": payload.Data.LimitReset})
}
