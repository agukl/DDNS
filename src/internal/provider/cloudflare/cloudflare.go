package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"ddns/internal/provider"
)

const defaultBaseURL = "https://api.cloudflare.com/client/v4"

type Provider struct {
	token       string
	client      *http.Client
	baseURL     string
	zoneIDCache map[string]string
}

func New(token string, client *http.Client) *Provider {
	return &Provider{
		token:       token,
		client:      client,
		baseURL:     defaultBaseURL,
		zoneIDCache: make(map[string]string),
	}
}

func (p *Provider) GetARecord(ctx context.Context, target provider.TargetRecord) (provider.DNSRecord, error) {
	zoneID, err := p.zoneID(ctx, target.Zone)
	if err != nil {
		return provider.DNSRecord{}, err
	}

	endpoint := fmt.Sprintf("%s/zones/%s/dns_records?type=A&name=%s",
		p.baseURL,
		url.PathEscape(zoneID),
		url.QueryEscape(target.Name),
	)
	var out recordListResponse
	if err := p.do(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return provider.DNSRecord{}, err
	}
	if !out.Success {
		return provider.DNSRecord{}, fmt.Errorf("cloudflare list records failed: %s", formatErrors(out.Errors))
	}
	if len(out.Result) == 0 {
		return provider.DNSRecord{}, provider.ErrRecordNotFound
	}
	if len(out.Result) > 1 {
		return provider.DNSRecord{}, provider.ErrMultipleRecords
	}

	record := out.Result[0]
	addr, err := netip.ParseAddr(strings.TrimSpace(record.Content))
	if err != nil {
		return provider.DNSRecord{}, fmt.Errorf("cloudflare returned invalid record content %q: %w", record.Content, err)
	}
	return provider.DNSRecord{
		ID:      record.ID,
		Zone:    target.Zone,
		Name:    record.Name,
		Type:    record.Type,
		Value:   addr.Unmap(),
		TTL:     record.TTL,
		Proxied: record.Proxied,
	}, nil
}

func (p *Provider) UpdateARecord(ctx context.Context, record provider.DNSRecord, value netip.Addr, ttl int) error {
	zoneID, err := p.zoneID(ctx, record.Zone)
	if err != nil {
		return err
	}
	if ttl <= 0 {
		ttl = record.TTL
	}
	if ttl <= 0 {
		ttl = 300
	}

	payload := updateRecordRequest{
		Type:    "A",
		Name:    record.Name,
		Content: value.String(),
		TTL:     ttl,
		Proxied: record.Proxied,
	}
	endpoint := fmt.Sprintf("%s/zones/%s/dns_records/%s",
		p.baseURL,
		url.PathEscape(zoneID),
		url.PathEscape(record.ID),
	)
	var out recordResponse
	if err := p.do(ctx, http.MethodPut, endpoint, payload, &out); err != nil {
		return err
	}
	if !out.Success {
		return fmt.Errorf("cloudflare update record failed: %s", formatErrors(out.Errors))
	}
	return nil
}

func (p *Provider) zoneID(ctx context.Context, zone string) (string, error) {
	zone = strings.ToLower(strings.TrimSpace(zone))
	if zone == "" {
		return "", fmt.Errorf("zone is required")
	}
	if id, ok := p.zoneIDCache[zone]; ok {
		return id, nil
	}

	endpoint := fmt.Sprintf("%s/zones?name=%s", p.baseURL, url.QueryEscape(zone))
	var out zoneListResponse
	if err := p.do(ctx, http.MethodGet, endpoint, nil, &out); err != nil {
		return "", err
	}
	if !out.Success {
		return "", fmt.Errorf("cloudflare list zones failed: %s", formatErrors(out.Errors))
	}
	if len(out.Result) == 0 {
		return "", fmt.Errorf("cloudflare zone %q not found", zone)
	}
	if len(out.Result) > 1 {
		return "", fmt.Errorf("cloudflare zone %q matched multiple zones", zone)
	}
	p.zoneIDCache[zone] = out.Result[0].ID
	return out.Result[0].ID, nil
}

func (p *Provider) do(ctx context.Context, method, endpoint string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "ddns/0.1")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.clientOrDefault().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("cloudflare api http %d: %s", resp.StatusCode, bodySnippet(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode cloudflare response: %w", err)
		}
	}
	return nil
}

func (p *Provider) clientOrDefault() *http.Client {
	if p.client != nil {
		return p.client
	}
	return &http.Client{Timeout: 10 * time.Second}
}

func bodySnippet(data []byte) string {
	text := strings.TrimSpace(string(data))
	if len(text) > 512 {
		return text[:512] + "..."
	}
	return text
}

func formatErrors(errs []cfError) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err.Message != "" {
			parts = append(parts, err.Message)
		}
	}
	if len(parts) == 0 {
		return "unknown error"
	}
	return strings.Join(parts, "; ")
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type zoneListResponse struct {
	Success bool      `json:"success"`
	Errors  []cfError `json:"errors"`
	Result  []cfZone  `json:"result"`
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type recordListResponse struct {
	Success bool       `json:"success"`
	Errors  []cfError  `json:"errors"`
	Result  []cfRecord `json:"result"`
}

type recordResponse struct {
	Success bool      `json:"success"`
	Errors  []cfError `json:"errors"`
	Result  cfRecord  `json:"result"`
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

type updateRecordRequest struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}
