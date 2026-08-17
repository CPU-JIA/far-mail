package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var domainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

type DNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority int    `json:"priority,omitempty"`
	Proxied  bool   `json:"proxied"`
}

type DNSAction struct {
	Record DNSRecord `json:"record"`
	Status string    `json:"status"`
	Detail string    `json:"detail,omitempty"`
	previous *cfDNSRecord
}

type DNSPlan struct {
	Domain string      `json:"domain"`
	ZoneID string      `json:"zone_id,omitempty"`
	Zone   string      `json:"zone,omitempty"`
	Items  []DNSAction `json:"items"`
	RolledBack bool    `json:"rolled_back,omitempty"`
	RollbackError string `json:"rollback_error,omitempty"`
}

func BuildDNSRecords(domain, smtpHostname, smtpServerIP string) ([]DNSRecord, error) {
	domain = normalizeDomain(domain)
	hostname := normalizeDomain(smtpHostname)
	if len(domain) > 253 || !domainPattern.MatchString(domain) {
		return nil, fmt.Errorf("invalid root domain")
	}
	if len(hostname) > 253 || !domainPattern.MatchString(hostname) {
		return nil, fmt.Errorf("SMTP hostname is not configured")
	}
	records := []DNSRecord{{Type: "MX", Name: domain, Content: hostname, Priority: 10}}
	if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
		if ip := net.ParseIP(strings.TrimSpace(smtpServerIP)); ip != nil {
			recordType := "AAAA"
			if ip.To4() != nil {
				recordType = "A"
			}
			records = append(records, DNSRecord{Type: recordType, Name: hostname, Content: ip.String()})
		}
	}
	return records, nil
}

type CloudflareClient struct {
	baseURL string
	client  *http.Client
}

func NewCloudflareClient() *CloudflareClient {
	return &CloudflareClient{baseURL: "https://api.cloudflare.com/client/v4", client: &http.Client{Timeout: 8 * time.Second}}
}

func (c *CloudflareClient) Test(ctx context.Context, token, domain string) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("Cloudflare API Token is not configured")
	}
	if strings.TrimSpace(domain) != "" {
		zoneID, zone, err := c.resolveZone(ctx, token, domain)
		if err != nil {
			return "", err
		}
		return zone + " (" + zoneID + ")", nil
	}
	var result map[string]any
	if err := c.request(ctx, token, http.MethodGet, "/user/tokens/verify", nil, &result); err != nil {
		return "", err
	}
	return "token active", nil
}

func (c *CloudflareClient) Preview(ctx context.Context, token, domain string, desired []DNSRecord) (DNSPlan, error) {
	zoneID, zone, err := c.resolveZone(ctx, token, domain)
	if err != nil {
		return DNSPlan{}, err
	}
	plan := DNSPlan{Domain: normalizeDomain(domain), ZoneID: zoneID, Zone: zone, Items: make([]DNSAction, 0, len(desired))}
	for _, record := range desired {
		existing, err := c.listRecords(ctx, token, zoneID, record.Type, record.Name)
		if err != nil {
			return plan, err
		}
		action := DNSAction{Record: record}
		switch {
		case len(existing) == 0:
			action.Status = "create"
		case len(existing) > 1:
			action.Status = "conflict"
			action.Detail = "multiple records already exist"
		case sameDNSRecord(existing[0], record):
			action.Status = "unchanged"
		default:
			action.Status = "conflict"
			action.Detail = "an existing record has a different value"
		}
		if len(existing) == 1 {
			previous := existing[0]
			action.previous = &previous
		}
		plan.Items = append(plan.Items, action)
	}
	return plan, nil
}

func (c *CloudflareClient) Apply(ctx context.Context, token, domain string, desired []DNSRecord, confirmConflicts bool) (DNSPlan, error) {
	plan, err := c.Preview(ctx, token, domain, desired)
	if err != nil {
		return plan, err
	}
	applied := make([]appliedChange, 0, len(plan.Items))
	for index := range plan.Items {
		action := &plan.Items[index]
		switch action.Status {
		case "unchanged":
			continue
		case "conflict":
			if !confirmConflicts || action.Detail == "multiple records already exist" {
				continue
			}
			existing, listErr := c.listRecords(ctx, token, plan.ZoneID, action.Record.Type, action.Record.Name)
			if listErr != nil || len(existing) != 1 {
				if listErr != nil {
					return plan, listErr
				}
				continue
			}
			if err := c.writeRecord(ctx, token, http.MethodPut, "/zones/"+plan.ZoneID+"/dns_records/"+existing[0].ID, action.Record); err != nil {
				return c.rollbackApply(ctx, token, plan, applied, err)
			}
			action.Status = "update"
			action.Detail = ""
			applied = append(applied, appliedChange{action: action, id: existing[0].ID})
		case "create":
			created, err := c.createRecord(ctx, token, plan.ZoneID, action.Record)
			if err != nil {
				return c.rollbackApply(ctx, token, plan, applied, err)
			}
			action.Status = "created"
			applied = append(applied, appliedChange{action: action, id: created.ID})
		}
	}
	return plan, nil
}

type appliedChange struct {
	action *DNSAction
	id     string
}

func (c *CloudflareClient) rollbackApply(ctx context.Context, token string, plan DNSPlan, applied []appliedChange, cause error) (DNSPlan, error) {
	var rollbackErr error
	for index := len(applied) - 1; index >= 0; index-- {
		change := applied[index]
		var err error
		if change.action.previous == nil {
			err = c.deleteRecord(ctx, token, plan.ZoneID, change.id)
		} else {
			err = c.writeRecord(ctx, token, http.MethodPut, "/zones/"+plan.ZoneID+"/dns_records/"+change.id, change.action.previous.toDNSRecord())
		}
		if err != nil {
			if rollbackErr == nil {
				rollbackErr = err
			}
			continue
		}
		change.action.Status = "rolled_back"
		change.action.Detail = "automatically reverted after a later DNS write failed"
	}
	if rollbackErr == nil {
		plan.RolledBack = true
		return plan, cause
	}
	plan.RollbackError = "automatic rollback could not restore every record"
	return plan, fmt.Errorf("Cloudflare apply failed and rollback was incomplete: %w", cause)
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDNSRecord struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	Priority int    `json:"priority"`
	Proxied  bool   `json:"proxied"`
}

func (record cfDNSRecord) toDNSRecord() DNSRecord {
	return DNSRecord{Type: record.Type, Name: record.Name, Content: record.Content, Priority: record.Priority, Proxied: record.Proxied}
}

func (c *CloudflareClient) resolveZone(ctx context.Context, token, domain string) (string, string, error) {
	domain = normalizeDomain(domain)
	if len(domain) > 253 || !domainPattern.MatchString(domain) {
		return "", "", fmt.Errorf("invalid root domain")
	}
	var zones []cfZone
	path := "/zones?name=" + url.QueryEscape(domain) + "&status=active&per_page=50"
	if err := c.request(ctx, token, http.MethodGet, path, nil, &zones); err != nil {
		return "", "", err
	}
	for _, zone := range zones {
		if strings.EqualFold(zone.Name, domain) {
			return zone.ID, zone.Name, nil
		}
	}
	return "", "", fmt.Errorf("Cloudflare Zone not found for %s", domain)
}

func (c *CloudflareClient) listRecords(ctx context.Context, token, zoneID, recordType, name string) ([]cfDNSRecord, error) {
	var records []cfDNSRecord
	path := "/zones/" + zoneID + "/dns_records?type=" + url.QueryEscape(recordType) + "&name=" + url.QueryEscape(name) + "&per_page=100"
	err := c.request(ctx, token, http.MethodGet, path, nil, &records)
	return records, err
}

func (c *CloudflareClient) writeRecord(ctx context.Context, token, method, path string, record DNSRecord) error {
	payload := map[string]any{"type": record.Type, "name": record.Name, "content": record.Content, "ttl": 1, "proxied": record.Proxied}
	if record.Type == "MX" {
		payload["priority"] = record.Priority
	}
	var result cfDNSRecord
	return c.request(ctx, token, method, path, payload, &result)
}

func (c *CloudflareClient) createRecord(ctx context.Context, token, zoneID string, record DNSRecord) (cfDNSRecord, error) {
	var result cfDNSRecord
	payload := map[string]any{"type": record.Type, "name": record.Name, "content": record.Content, "ttl": 1, "proxied": record.Proxied}
	if record.Type == "MX" {
		payload["priority"] = record.Priority
	}
	err := c.request(ctx, token, http.MethodPost, "/zones/"+zoneID+"/dns_records", payload, &result)
	return result, err
}

func (c *CloudflareClient) deleteRecord(ctx context.Context, token, zoneID, recordID string) error {
	return c.request(ctx, token, http.MethodDelete, "/zones/"+zoneID+"/dns_records/"+recordID, nil, nil)
}

func (c *CloudflareClient) request(ctx context.Context, token, method, path string, payload any, target any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("Content-Type", "application/json")
	response, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare request failed")
	}
	defer response.Body.Close()
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
		Errors  []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return fmt.Errorf("invalid Cloudflare response")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || !envelope.Success {
		message := "Cloudflare API rejected the request"
		if len(envelope.Errors) > 0 && envelope.Errors[0].Message != "" {
			message = envelope.Errors[0].Message
		}
		return fmt.Errorf("%s", message)
	}
	if target != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, target); err != nil {
			return fmt.Errorf("invalid Cloudflare result")
		}
	}
	return nil
}

func sameDNSRecord(current cfDNSRecord, desired DNSRecord) bool {
	currentContent := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(current.Content)), ".")
	desiredContent := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(desired.Content)), ".")
	if !strings.EqualFold(current.Type, desired.Type) || currentContent != desiredContent {
		return false
	}
	if current.Proxied != desired.Proxied {
		return false
	}
	return desired.Type != "MX" || current.Priority == desired.Priority
}

func normalizeDomain(value string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
}
