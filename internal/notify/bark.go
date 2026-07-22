package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/guchengod/compose-updater/internal/config"
)

type Notifier interface {
	Enabled() bool
	Send(ctx context.Context, title, body, level string) error
}

type Noop struct{}

func (Noop) Enabled() bool                                      { return false }
func (Noop) Send(context.Context, string, string, string) error { return nil }

type Bark struct {
	cfg      config.BarkConfig
	endpoint string
	client   *http.Client
}

type barkRequest struct {
	DeviceKey string `json:"device_key"`
	Title     string `json:"title,omitempty"`
	Body      string `json:"body"`
	Group     string `json:"group,omitempty"`
	Level     string `json:"level,omitempty"`
}

type barkResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewBark(cfg config.BarkConfig) (*Bark, error) {
	endpoint, err := normalizeEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	return &Bark{cfg: cfg, endpoint: endpoint, client: &http.Client{Timeout: cfg.RequestTimeout()}}, nil
}

func (b *Bark) Enabled() bool { return b != nil && b.cfg.Enabled }

func (b *Bark) Send(ctx context.Context, title, body, level string) error {
	if !b.Enabled() {
		return nil
	}
	payload := barkRequest{
		DeviceKey: b.cfg.DeviceKey,
		Title:     title,
		Body:      truncateRunes(body, 3500),
		Group:     b.cfg.Group,
		Level:     level,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("编码 Bark 请求: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("创建 Bark 请求: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	resp, err := b.client.Do(req)
	if err != nil {
		return fmt.Errorf("发送 Bark 请求: %w", err)
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return fmt.Errorf("读取 Bark 响应: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Bark HTTP 状态 %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	var parsed barkResponse
	if len(bytes.TrimSpace(responseBody)) > 0 && json.Unmarshal(responseBody, &parsed) == nil {
		if parsed.Code != 0 && parsed.Code != 200 {
			return fmt.Errorf("Bark 返回错误 code=%d message=%s", parsed.Code, parsed.Message)
		}
	}
	return nil
}

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("无效 Bark endpoint %q", raw)
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/push") && path != "push" {
		path += "/push"
	}
	if path == "push" {
		path = "/push"
	}
	u.Path = path
	return u.String(), nil
}

func truncateRunes(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "…"
}
