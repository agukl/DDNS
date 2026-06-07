package dnspod

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
	"net/netip"
	"strconv"
	"strings"
	"time"

	"ddns/internal/provider"
)

const (
	host        = "dnspod.tencentcloudapi.com"
	endpoint    = "https://" + host
	service     = "dnspod"
	version     = "2021-03-23"
	contentType = "application/json; charset=utf-8"
	algorithm   = "TC3-HMAC-SHA256"
)

type Provider struct {
	secretID  string
	secretKey string
	client    *http.Client
	now       func() time.Time
}

func New(secretID, secretKey string, client *http.Client) *Provider {
	return &Provider{
		secretID:  secretID,
		secretKey: secretKey,
		client:    client,
		now:       time.Now,
	}
}

func (p *Provider) GetARecord(ctx context.Context, target provider.TargetRecord) (provider.DNSRecord, error) {
	payload := describeRecordListRequest{
		Domain:       target.Zone,
		Subdomain:    subdomain(target),
		RecordType:   "A",
		RecordLine:   target.RecordLine,
		RecordLineID: target.RecordLineID,
		Limit:        100,
		ErrorOnEmpty: "no",
	}

	var out describeRecordListResponse
	if err := p.do(ctx, "DescribeRecordList", payload, &out); err != nil {
		return provider.DNSRecord{}, err
	}
	if out.Response.Error != nil {
		return provider.DNSRecord{}, fmt.Errorf("dnspod describe record list failed: %s: %s", out.Response.Error.Code, out.Response.Error.Message)
	}

	var matches []recordListItem
	for _, record := range out.Response.RecordList {
		if strings.EqualFold(record.Type, "A") && record.Name == payload.Subdomain {
			matches = append(matches, record)
		}
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
		return provider.DNSRecord{}, fmt.Errorf("dnspod returned invalid record value %q: %w", record.Value, err)
	}
	return provider.DNSRecord{
		ID:           strconv.FormatInt(record.RecordID, 10),
		Zone:         target.Zone,
		Name:         target.Name,
		Type:         record.Type,
		Value:        addr.Unmap(),
		TTL:          record.TTL,
		RecordLine:   record.Line,
		RecordLineID: record.LineID,
	}, nil
}

func (p *Provider) UpdateARecord(ctx context.Context, record provider.DNSRecord, value netip.Addr, ttl int) error {
	recordID, err := strconv.ParseInt(record.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid dnspod record id %q: %w", record.ID, err)
	}
	if ttl <= 0 {
		ttl = record.TTL
	}
	if ttl <= 0 {
		ttl = 300
	}
	recordLine := record.RecordLine
	if recordLine == "" {
		recordLine = "默认"
	}

	payload := modifyRecordRequest{
		Domain:       record.Zone,
		SubDomain:    subdomainFromName(record.Zone, record.Name),
		RecordType:   "A",
		RecordLine:   recordLine,
		RecordLineID: record.RecordLineID,
		Value:        value.String(),
		RecordID:     recordID,
		TTL:          ttl,
	}

	var out modifyRecordResponse
	if err := p.do(ctx, "ModifyRecord", payload, &out); err != nil {
		return err
	}
	if out.Response.Error != nil {
		return fmt.Errorf("dnspod modify record failed: %s: %s", out.Response.Error.Code, out.Response.Error.Message)
	}
	return nil
}

func (p *Provider) do(ctx context.Context, action string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := p.now().Unix()
	req.Header.Set("Authorization", p.authorization(action, string(body), timestamp))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Language", "zh-CN")
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
		return fmt.Errorf("dnspod api http %d: %s", resp.StatusCode, bodySnippet(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode dnspod response: %w", err)
		}
	}
	return nil
}

func (p *Provider) authorization(action, payload string, timestamp int64) string {
	date := time.Unix(timestamp, 0).UTC().Format("2006-01-02")
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n"
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		"/",
		"",
		canonicalHeaders,
		signedHeaders,
		sha256Hex(payload),
	}, "\n")

	credentialScope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		algorithm,
		strconv.FormatInt(timestamp, 10),
		credentialScope,
		sha256Hex(canonicalRequest),
	}, "\n")

	secretDate := hmacSHA256([]byte("TC3"+p.secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	return fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		algorithm,
		p.secretID,
		credentialScope,
		signedHeaders,
		signature,
	)
}

func subdomain(target provider.TargetRecord) string {
	name := strings.TrimSpace(target.Name)
	if name == "" || name == "@" {
		return "@"
	}
	return subdomainFromName(target.Zone, name)
}

func subdomainFromName(zone, name string) string {
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

func hmacSHA256(key []byte, msg string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(msg))
	return mac.Sum(nil)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
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

type describeRecordListRequest struct {
	Domain       string `json:"Domain"`
	Subdomain    string `json:"Subdomain,omitempty"`
	RecordType   string `json:"RecordType,omitempty"`
	RecordLine   string `json:"RecordLine,omitempty"`
	RecordLineID string `json:"RecordLineId,omitempty"`
	Limit        int    `json:"Limit,omitempty"`
	ErrorOnEmpty string `json:"ErrorOnEmpty,omitempty"`
}

type describeRecordListResponse struct {
	Response struct {
		Error      *apiError        `json:"Error,omitempty"`
		RecordList []recordListItem `json:"RecordList"`
		RequestID  string           `json:"RequestId"`
	} `json:"Response"`
}

type recordListItem struct {
	RecordID int64  `json:"RecordId"`
	Value    string `json:"Value"`
	Status   string `json:"Status"`
	Name     string `json:"Name"`
	Line     string `json:"Line"`
	LineID   string `json:"LineId"`
	Type     string `json:"Type"`
	TTL      int    `json:"TTL"`
	MX       int    `json:"MX"`
}

type modifyRecordRequest struct {
	Domain       string `json:"Domain"`
	SubDomain    string `json:"SubDomain,omitempty"`
	RecordType   string `json:"RecordType"`
	RecordLine   string `json:"RecordLine"`
	RecordLineID string `json:"RecordLineId,omitempty"`
	Value        string `json:"Value"`
	RecordID     int64  `json:"RecordId"`
	TTL          int    `json:"TTL,omitempty"`
}

type modifyRecordResponse struct {
	Response struct {
		Error     *apiError `json:"Error,omitempty"`
		RecordID  int64     `json:"RecordId"`
		RequestID string    `json:"RequestId"`
	} `json:"Response"`
}

type apiError struct {
	Code    string `json:"Code"`
	Message string `json:"Message"`
}
