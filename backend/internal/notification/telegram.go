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

// TelegramNotifier sends alerts via Telegram Bot API.
type TelegramNotifier struct {
	botToken string
	chatID   string
	client   *http.Client
}

// NewTelegramNotifier creates a Telegram notifier.
// botToken: Bot API token from @BotFather
// chatID: Target chat/group/channel ID
func NewTelegramNotifier(botToken, chatID string) *TelegramNotifier {
	return &TelegramNotifier{
		botToken: botToken,
		chatID:   chatID,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (t *TelegramNotifier) Send(ctx context.Context, alert Alert) error {
	emoji := "ℹ️"
	switch alert.Level {
	case AlertWarning:
		emoji = "⚠️"
	case AlertCritical:
		emoji = "🚨"
	}

	text := fmt.Sprintf("%s *%s*\n\n%s", emoji, escapeMarkdown(alert.Title), escapeMarkdown(alert.Message))

	body, _ := json.Marshal(map[string]interface{}{
		"chat_id":    t.chatID,
		"text":       text,
		"parse_mode": "MarkdownV2",
	})

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram: unexpected status %d", resp.StatusCode)
	}

	log.Printf("[telegram] sent alert: %s", alert.Title)
	return nil
}

// escapeMarkdown escapes special characters for Telegram MarkdownV2.
func escapeMarkdown(s string) string {
	specials := []byte{'_', '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}', '.', '!'}
	var buf bytes.Buffer
	for i := 0; i < len(s); i++ {
		for _, sp := range specials {
			if s[i] == sp {
				buf.WriteByte('\\')
				break
			}
		}
		buf.WriteByte(s[i])
	}
	return buf.String()
}
