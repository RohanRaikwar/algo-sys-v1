package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// WebhookNotifier sends alerts to a generic HTTP webhook endpoint.
type WebhookNotifier struct {
	url    string
	client *http.Client
}

// NewWebhookNotifier creates a webhook notifier.
// url: The HTTP endpoint to POST alerts to.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		url: url,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (w *WebhookNotifier) Send(ctx context.Context, alert Alert) error {
	payload := map[string]interface{}{
		"level":   string(alert.Level),
		"title":   alert.Title,
		"message": alert.Message,
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook: unexpected status %d", resp.StatusCode)
	}

	log.Printf("[webhook] sent alert to %s: %s", w.url, alert.Title)
	return nil
}
