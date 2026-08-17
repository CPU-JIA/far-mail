package integrations

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type GenericWebhookConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
	Secret  string `json:"secret,omitempty"`
}

type TelegramConfig struct {
	Enabled  bool   `json:"enabled"`
	BotToken string `json:"bot_token,omitempty"`
	ChatID   string `json:"chat_id,omitempty"`
}

type DiscordConfig struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
}

type NotificationConfig struct {
	Generic  GenericWebhookConfig `json:"generic"`
	Telegram TelegramConfig       `json:"telegram"`
	Discord  DiscordConfig        `json:"discord"`
}

type CloudflareConfig struct {
	APIToken string `json:"api_token,omitempty"`
}

type secretData struct {
	Notifications NotificationConfig `json:"notifications"`
	Cloudflare    CloudflareConfig   `json:"cloudflare"`
}

type SecretStore struct {
	mu   sync.RWMutex
	path string
	data secretData
}

func NewSecretStore(path string) (*SecretStore, error) {
	if path == "" {
		path = "/data/integrations.json"
	}
	s := &SecretStore{path: path}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &s.data); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *SecretStore) Notifications() NotificationConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Notifications
}

func (s *SecretStore) Cloudflare() (CloudflareConfig, string) {
	if token := os.Getenv("CLOUDFLARE_API_TOKEN"); token != "" {
		return CloudflareConfig{APIToken: token}, "environment"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data.Cloudflare, "file"
}

func (s *SecretStore) UpdateNotifications(update func(*NotificationConfig)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	update(&s.data.Notifications)
	return s.persistLocked()
}

func (s *SecretStore) SetCloudflare(config CloudflareConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Cloudflare = config
	return s.persistLocked()
}

func (s *SecretStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
