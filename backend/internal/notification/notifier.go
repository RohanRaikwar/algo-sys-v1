// Package notification provides alert delivery to external channels
// (Telegram, Discord, webhooks, etc.) for trading events.
package notification

import (
	"context"
	"log"
)

// AlertLevel represents the severity of an alert.
type AlertLevel string

const (
	AlertInfo     AlertLevel = "INFO"
	AlertWarning  AlertLevel = "WARNING"
	AlertCritical AlertLevel = "CRITICAL"
)

// Alert represents a notification to be sent.
type Alert struct {
	Level   AlertLevel `json:"level"`
	Title   string     `json:"title"`
	Message string     `json:"message"`
}

// Notifier is the interface for all notification backends.
type Notifier interface {
	// Send delivers an alert. Returns error if delivery fails.
	Send(ctx context.Context, alert Alert) error
}

// LogNotifier is a simple notifier that logs alerts (useful for development).
type LogNotifier struct{}

// NewLogNotifier creates a log-based notifier.
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

func (n *LogNotifier) Send(ctx context.Context, alert Alert) error {
	log.Printf("[notify] [%s] %s: %s", alert.Level, alert.Title, alert.Message)
	return nil
}

// TODO: Implement TelegramNotifier, DiscordNotifier, WebhookNotifier
