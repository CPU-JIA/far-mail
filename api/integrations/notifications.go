package integrations

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"farmail/model"
)

type DeliveryStatus struct {
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	LastChannel   string     `json:"last_channel,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	Queued        int        `json:"queued"`
	Dropped       uint64     `json:"dropped"`
}

type Dispatcher struct {
	store   *SecretStore
	client  *http.Client
	queue   chan notificationEvent
	dropped atomic.Uint64
	mu      sync.RWMutex
	status  DeliveryStatus
}

type notificationEvent struct {
	Name       string
	OccurredAt time.Time
	Email      *model.MailboxEmailEvent
	Domain     string
	Detail     string
}

func NewDispatcher(store *SecretStore) *Dispatcher {
	return &Dispatcher{
		store:  store,
		client: &http.Client{Timeout: 5 * time.Second},
		queue:  make(chan notificationEvent, 128),
	}
}

func (d *Dispatcher) Start(ctx context.Context, events <-chan model.MailboxEmailEvent, unsubscribe func()) {
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				select {
				case d.queue <- emailNotification(event):
				default:
					d.dropped.Add(1)
				}
			}
		}
	}()
	for range 2 {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case event := <-d.queue:
					d.deliverEmail(ctx, event)
				}
			}
		}()
	}
}

// PublishDomain queues a domain lifecycle event without blocking the verifier.
func (d *Dispatcher) PublishDomain(name, domain, detail string) {
	event := notificationEvent{Name: name, OccurredAt: time.Now(), Domain: domain, Detail: detail}
	select {
	case d.queue <- event:
	default:
		d.dropped.Add(1)
	}
}

func (d *Dispatcher) Status() DeliveryStatus {
	d.mu.RLock()
	defer d.mu.RUnlock()
	status := d.status
	status.Queued = len(d.queue)
	status.Dropped = d.dropped.Load()
	return status
}

func (d *Dispatcher) Test(ctx context.Context, channel string) error {
	now := time.Now()
	event := model.MailboxEmailEvent{
		FullAddress: "inbox@example.com",
		Email:       model.EmailSummary{Sender: "notify@example.com", Subject: "FAR Mail notification test", ReceivedAt: now},
	}
	return d.deliverChannel(ctx, channel, emailNotification(event), true)
}

func (d *Dispatcher) deliverEmail(ctx context.Context, event notificationEvent) {
	config := d.store.Notifications()
	if config.Generic.Enabled {
		_ = d.deliverChannel(ctx, "generic", event, false)
	}
	if config.Telegram.Enabled {
		_ = d.deliverChannel(ctx, "telegram", event, false)
	}
	if config.Discord.Enabled {
		_ = d.deliverChannel(ctx, "discord", event, false)
	}
}

func (d *Dispatcher) deliverChannel(ctx context.Context, channel string, event notificationEvent, test bool) error {
	config := d.store.Notifications()
	payload := map[string]any{
		"event": event.Name, "test": test, "occurred_at": event.OccurredAt,
	}
	if event.Email != nil {
		payload["email"] = map[string]any{
			"id": event.Email.Email.ID, "mailbox": event.Email.FullAddress,
			"sender": event.Email.Email.Sender, "subject": event.Email.Email.Subject,
			"has_attachments": event.Email.Email.HasAttachments,
			"parsed_code":     event.Email.Email.ParsedCode, "parsed_link": event.Email.Email.ParsedLink,
		}
	} else {
		payload["domain"] = map[string]any{"name": event.Domain, "detail": event.Detail}
	}
	var endpoint string
	var body any = payload
	var signatureSecret string
	switch channel {
	case "generic":
		endpoint = strings.TrimSpace(config.Generic.URL)
		signatureSecret = config.Generic.Secret
	case "telegram":
		if config.Telegram.BotToken != "" {
			endpoint = "https://api.telegram.org/bot" + config.Telegram.BotToken + "/sendMessage"
		}
		body = map[string]any{"chat_id": config.Telegram.ChatID, "text": notificationText(event, test), "disable_web_page_preview": true}
	case "discord":
		endpoint = strings.TrimSpace(config.Discord.URL)
		body = map[string]any{"content": notificationText(event, test), "allowed_mentions": map[string]any{"parse": []string{}}}
	default:
		return fmt.Errorf("unsupported notification channel")
	}
	if err := validateHTTPURL(endpoint); err != nil {
		return fmt.Errorf("%s is not configured", channel)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var deliveryErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<attempt) * 250 * time.Millisecond):
			}
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
		if reqErr != nil {
			return reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "FAR-Mail-Notifier/1.0")
		if signatureSecret != "" {
			mac := hmac.New(sha256.New, []byte(signatureSecret))
			_, _ = mac.Write(raw)
			req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		}
		response, requestErr := d.client.Do(req)
		if requestErr != nil {
			deliveryErr = fmt.Errorf("delivery request failed")
			continue
		}
		_, _ = io.CopyN(io.Discard, response.Body, 4096)
		_ = response.Body.Close()
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			d.recordDelivery(channel, nil)
			return nil
		}
		deliveryErr = fmt.Errorf("remote service returned HTTP %d", response.StatusCode)
		if response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			break
		}
	}
	d.recordDelivery(channel, deliveryErr)
	return deliveryErr
}

func (d *Dispatcher) recordDelivery(channel string, err error) {
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.status.LastAttemptAt = &now
	d.status.LastChannel = channel
	if err == nil {
		d.status.LastSuccessAt = &now
		d.status.LastError = ""
	} else {
		d.status.LastError = err.Error()
	}
}

func notificationText(event notificationEvent, test bool) string {
	if event.Email == nil {
		return fmt.Sprintf("Domain alert\nEvent: %s\nDomain: %s\nDetail: %s", event.Name, event.Domain, event.Detail)
	}
	prefix := "New email"
	if test {
		prefix = "Notification test"
	}
	text := fmt.Sprintf("%s\nMailbox: %s\nFrom: %s\nSubject: %s", prefix, event.Email.FullAddress, event.Email.Email.Sender, event.Email.Email.Subject)
	if event.Email.Email.ParsedCode != "" {
		text += "\nCode: " + event.Email.Email.ParsedCode
	}
	if event.Email.Email.ParsedLink != "" {
		text += "\nLink: " + event.Email.Email.ParsedLink
	}
	return text
}

func emailNotification(event model.MailboxEmailEvent) notificationEvent {
	return notificationEvent{Name: "email.received", OccurredAt: event.Email.ReceivedAt, Email: &event}
}

func validateHTTPURL(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid HTTP URL")
	}
	return nil
}
