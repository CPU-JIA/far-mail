package integrations

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSecretStorePersistsAndDoesNotLoseOtherChannels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "integrations.json")
	secrets, err := NewSecretStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := secrets.UpdateNotifications(func(config *NotificationConfig) {
		config.Generic = GenericWebhookConfig{Enabled: true, URL: "https://example.com/hook", Secret: "secret"}
	}); err != nil {
		t.Fatal(err)
	}
	if err := secrets.SetCloudflare(CloudflareConfig{APIToken: "cloudflare-secret"}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewSecretStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Notifications().Generic.Secret != "secret" {
		t.Fatal("webhook secret was not persisted")
	}
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	cloudflare, source := reloaded.Cloudflare()
	if cloudflare.APIToken != "cloudflare-secret" || source != "file" {
		t.Fatalf("unexpected Cloudflare config: %#v, %s", cloudflare, source)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		t.Fatalf("secret file permissions are too broad: %v", info.Mode().Perm())
	}
}
