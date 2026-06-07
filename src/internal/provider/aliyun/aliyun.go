package aliyun

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"ddns/internal/provider"
)

const (
	endpoint = "https://alidns.aliyuncs.com/"
	version  = "2015-01-09"
)

type Provider struct {
	accessKeyID     string
	accessKeySecret string
	client          *http.Client
	now             func() time.Time
	nonce           func() string
}

func New(accessKeyID, accessKeySecret string, client *http.Client) *Provider {
	return &Provider{
		accessKeyID:     accessKeyID,
		accessKeySecret: accessKeySecret,
		client:          client,
		now:             time.Now,
		nonce:           randomNonce,
	}
}

func (p *Provider) GetARecord(ctx context.Context, target provider.TargetRecord) (provider.DNSRecord, error) {
	params := map[string]string{
		"Action":     "DescribeSubDomainRecords",
		"DomainName": target.Zone,
		"SubDomain":  fullName(target.Zone, target.Name),
		"Type":       "A",
		"PageSize":   "100",
	}
	if target.RecordLine != "" {
		params["Line"] = target.RecordLine
	}

	var out describeSubDomainRecordsResponse
	if err := p.do(ctx, params, &out); err != nil {
		return provider.DNSRecord{}, err
	}

	var matches []aliyunRecord
	for _, record := range out.DomainRecords.Record {
		if !strings.EqualFold(record.Type, "A") {
			continue
		}
		if target.RecordLine != "" && record.Line != target.RecordLine {
			continue
		}
		matches = append(matches, record)
	}
	if len(matches) == 0 {
		return provider.DNSRecord{}, provider.ErrRecordNotFound
	}
	if len(matches) > 1 {
		return provider.DNSRecord{}, provider.ErrMultipleRecords
	}

	record := matches[0]
	addr, err := netip.ParseAddr(strings.TrimSpace(record.Value))
	if err != nil {
		return provider.DNSRecord{}, fmt.Errorf("aliyun returned invalid record value %q: %w", record.Value, err)
	}
	return provider.DNSRecord{
		ID:         record.RecordID,
		Zone:       target.Zone,
		Name:       fullName(target.Zone, target.Name),
		Type:       record.Type,
		Value:      addr.Unmap(),
		TTL:        record.TTL,
		RecordLine: record.Line,
	}, nil
}

func (p *Provider) UpdateARecord(ctx context.Context, record provider.DNSRecord, value netip.Addr, ttl int) error {
	if ttl <= 0 {
		ttl = record.TTL
	}
	if ttl <= 0 {
		ttl = 600
	}
	rr := rrFromName(record.Zone, record.Name)
	line := record.RecordLine
	if line == "" {
		line = "default"
	}

	params := map[string]string{
		"Action":   "UpdateDomainRecord",
		"RecordId": record.ID,
		"RR":       rr,
		"Type":     "A",
		"Value":    value.String(),
		"TTL":      strconv.Itoa(ttl),
		"Line":     line,
	}

	var out updateDomainRecordResponse
	if err := p.do(ctx, params, &out); err != nil {
		return err
	}
	if out.RecordID == "" {
		return fmt.Errorf("aliyun update record returned empty record id")
	}
	return nil
}

func (p *Provider) do(ctx context.Context, actionParams map[string]string, out any) error {
	params := p.commonParams()
	for key, value := range actionParams {
		if value != "" {
			params[key] = value
		}
	}
	params["Signature"] = p.signature(params)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+canonicalQuery(params), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ddns/0.1")

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
		var apiErr apiErrorResponse
		if err := json.Unmarshal(data, &apiErr); err == nil && apiErr.Code != "" {
			return fmt.Errorf("aliyun api http %d: %s: %s", resp.StatusCode, apiErr.Code, apiErr.Message)
		}
		return fmt.Errorf("aliyun api http %d: %s", resp.StatusCode, bodySnippet(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode aliyun response: %w", err)
		}
	}
	return nil
}

func (p *Provider) commonParams() map[string]string {
	return map[string]string{
		"Format":           "JSON",
		"Version":          version,
		"AccessKeyId":      p.accessKeyID,
		"SignatureMethod":  "HMAC-SHA1",
		"Timestamp":        p.now().UTC().Format("2006-01-02T15:04:05Z"),
		"SignatureVersion": "1.0",
		"SignatureNonce":   p.nonce(),
	}
}

func (p *Provider) signature(params map[string]string) string {
	canonical := canonicalQuery(params)
	stringToSign := http.MethodGet + "&%2F&" + percentEncode(canonical)
	key := []byte(p.accessKeySecret + "&")
	mac := hmac.New(sha1.New, key)
	mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func canonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, percentEncode(key)+"="+percentEncode(params[key]))
	}
	return strings.Join(parts, "&")
}

func percentEncode(s string) string {
	encoded := url.QueryEscape(s)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}

func fullName(zone, name string) string {
	zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || name == "@" || name == zone {
		return zone
	}
	if strings.HasSuffix(name, "."+zone) {
		return name
	}
	return name + "." + zone
}

func rrFromName(zone, name string) string {
	zone = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(zone)), ".")
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if name == "" || name == "@" || name == zone {
		return "@"
	}
	suffix := "." + zone
	if strings.HasSuffix(name, suffix) {
		return strings.TrimSuffix(name, suffix)
	}
	return name
}

func randomNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return hex.EncodeToString(b[:])
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

type describeSubDomainRecordsResponse struct {
	RequestID     string `json:"RequestId"`
	TotalCount    int    `json:"TotalCount"`
	DomainRecords struct {
		Record []aliyunRecord `json:"Record"`
	} `json:"DomainRecords"`
}

type aliyunRecord struct {
	RecordID   string `json:"RecordId"`
	RR         string `json:"RR"`
	DomainName string `json:"DomainName"`
	Type       string `json:"Type"`
	Value      string `json:"Value"`
	TTL        int    `json:"TTL"`
	Line       string `json:"Line"`
	Status     string `json:"Status"`
}

type updateDomainRecordResponse struct {
	RequestID string `json:"RequestId"`
	RecordID  string `json:"RecordId"`
}

type apiErrorResponse struct {
	RequestID string `json:"RequestId"`
	Code      string `json:"Code"`
	Message   string `json:"Message"`
}
