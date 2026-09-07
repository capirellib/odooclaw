package metering

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type cachedAuthorization struct {
	response AuthorizeResponse
	expires  time.Time
}

type controlClient struct {
	baseURL      string
	database     string
	serviceToken string
	http         *http.Client
	mu           sync.Mutex
	cache        map[string]cachedAuthorization
}

func newControlClient(baseURL, database, serviceToken string, timeout time.Duration) *controlClient {
	return &controlClient{
		baseURL: strings.TrimRight(baseURL, "/"), database: strings.TrimSpace(database),
		serviceToken: serviceToken, http: &http.Client{Timeout: timeout},
		cache: make(map[string]cachedAuthorization),
	}
}

func (c *controlClient) endpoint(path string) string {
	u := c.baseURL + path
	if c.database == "" {
		return u
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	q := parsed.Query()
	q.Set("db", c.database)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

func tokenCacheKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c *controlClient) authorize(ctx context.Context, token string) (AuthorizeResponse, error) {
	key := tokenCacheKey(token)
	now := time.Now()
	c.mu.Lock()
	if cached, ok := c.cache[key]; ok && now.Before(cached.expires) {
		c.mu.Unlock()
		return cached.response, nil
	}
	c.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"token": token})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/api/v1/authorize"), bytes.NewReader(body))
	if err != nil {
		return AuthorizeResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Betta-Service-Token", c.serviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return AuthorizeResponse{}, fmt.Errorf("authorize request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return AuthorizeResponse{}, fmt.Errorf("authorize response: %w", err)
	}
	var result AuthorizeResponse
	if err := json.Unmarshal(raw, &result); err != nil {
		return AuthorizeResponse{}, fmt.Errorf("authorize returned HTTP %d with invalid JSON: %w", resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("authorize returned HTTP %d: %s", resp.StatusCode, result.Error)
	}
	if result.TTL > 0 {
		c.mu.Lock()
		c.cache[key] = cachedAuthorization{response: result, expires: now.Add(time.Duration(result.TTL) * time.Second)}
		c.mu.Unlock()
	}
	return result, nil
}

func (c *controlClient) sendUsage(ctx context.Context, payload []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint("/api/v1/usage"), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Betta-Service-Token", c.serviceToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("usage returned invalid JSON (HTTP %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		return fmt.Errorf("usage rejected (HTTP %d): %s", resp.StatusCode, result.Error)
	}
	return nil
}
