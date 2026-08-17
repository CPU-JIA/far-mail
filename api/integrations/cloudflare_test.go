package integrations

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestBuildDNSRecordsUsesDeploymentValues(t *testing.T) {
	records, err := BuildDNSRecords("example.com", "mail.example.com", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("expected MX and A records, got %d", len(records))
	}
	if records[0].Type != "MX" || records[0].Name != "example.com" || records[0].Content != "mail.example.com" || records[0].Priority != 10 {
		t.Fatalf("unexpected MX record: %#v", records[0])
	}
	if records[1].Type != "A" || records[1].Name != "mail.example.com" || records[1].Content != "203.0.113.10" {
		t.Fatalf("unexpected A record: %#v", records[1])
	}
}

func TestBuildDNSRecordsDoesNotManageExternalSMTPHostname(t *testing.T) {
	records, err := BuildDNSRecords("example.com", "mail.hosted.example.net", "203.0.113.10")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Type != "MX" {
		t.Fatalf("expected only an MX record, got %#v", records)
	}
}

func TestBuildDNSRecordsRejectsMissingDeploymentHostname(t *testing.T) {
	if _, err := BuildDNSRecords("example.com", "", "203.0.113.10"); err == nil {
		t.Fatal("expected missing SMTP hostname to be rejected")
	}
}

func TestSameDNSRecordNormalizesTrailingDot(t *testing.T) {
	current := cfDNSRecord{Type: "MX", Content: "mail.example.com.", Priority: 10}
	desired := DNSRecord{Type: "MX", Content: "mail.example.com", Priority: 10}
	if !sameDNSRecord(current, desired) {
		t.Fatal("equivalent MX records should be unchanged")
	}
}

func TestCloudflareApplyRollsBackEarlierWritesWhenLaterWriteFails(t *testing.T) {
	var mu sync.Mutex
	createCount := 0
	deleted := make([]string, 0, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(status int, success bool, result any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": success, "result": result})
		}
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/zones") && !strings.Contains(r.URL.Path, "dns_records"):
			write(http.StatusOK, true, []cfZone{{ID: "zone-1", Name: "example.com"}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/dns_records"):
			write(http.StatusOK, true, []cfDNSRecord{})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/dns_records"):
			mu.Lock()
			createCount++
			current := createCount
			mu.Unlock()
			if current == 1 {
				write(http.StatusOK, true, cfDNSRecord{ID: "mx-1", Type: "MX", Name: "example.com", Content: "mail.example.com", Priority: 10})
				return
			}
			write(http.StatusBadGateway, false, nil)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/dns_records/"):
			mu.Lock()
			deleted = append(deleted, r.URL.Path)
			mu.Unlock()
			write(http.StatusOK, true, nil)
		default:
			write(http.StatusNotFound, false, nil)
		}
	}))
	defer server.Close()

	client := NewCloudflareClient()
	client.baseURL = server.URL
	plan, err := client.Apply(t.Context(), "token-for-test", "example.com", []DNSRecord{
		{Type: "MX", Name: "example.com", Content: "mail.example.com", Priority: 10},
		{Type: "A", Name: "mail.example.com", Content: "203.0.113.10"},
	}, false)
	if err == nil {
		t.Fatal("expected the second DNS write to fail")
	}
	if !plan.RolledBack || plan.RollbackError != "" {
		t.Fatalf("expected a clean automatic rollback, got %#v", plan)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != 1 || !strings.HasSuffix(deleted[0], "/dns_records/mx-1") {
		t.Fatalf("expected the first record to be deleted during rollback, got %#v", deleted)
	}
}
