package ipgetter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"ddns/internal/provider"
)

type Getter struct {
	Client    *http.Client
	Endpoints []string
	IPType    string
	Timeout   time.Duration
	Providers *provider.Registry
}

func (g *Getter) GetOutboundIP(ctx context.Context) (netip.Addr, error) {
	if len(g.Endpoints) == 0 {
		return netip.Addr{}, fmt.Errorf("no public ip endpoints configured")
	}

	var errs []error
	for _, endpoint := range g.Endpoints {
		addr, err := g.tryEndpoint(ctx, endpoint)
		if err == nil {
			return addr, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", endpoint, err))
	}
	return netip.Addr{}, fmt.Errorf("get outbound ip failed: %w", errors.Join(errs...))
}

func (g *Getter) GetARecord(ctx context.Context, target provider.TargetRecord) (provider.DNSRecord, error) {
	if strings.ToUpper(target.Type) != "A" {
		return provider.DNSRecord{}, fmt.Errorf("record type %q is not supported yet", target.Type)
	}
	p, err := g.Providers.Get(target.Provider)
	if err != nil {
		return provider.DNSRecord{}, err
	}
	return p.GetARecord(ctx, target)
}

func (g *Getter) tryEndpoint(ctx context.Context, endpoint string) (netip.Addr, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("User-Agent", "ddns/0.1")

	resp, err := g.client().Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return netip.Addr{}, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return netip.Addr{}, err
	}
	text := strings.TrimSpace(string(body))
	addr, err := netip.ParseAddr(text)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid ip %q", text)
	}
	addr = addr.Unmap()

	switch strings.ToLower(g.IPType) {
	case "ipv4", "":
		if !addr.Is4() {
			return netip.Addr{}, fmt.Errorf("expected ipv4, got %s", addr)
		}
	case "ipv6":
		if !addr.Is6() || addr.Is4() {
			return netip.Addr{}, fmt.Errorf("expected ipv6, got %s", addr)
		}
	default:
		return netip.Addr{}, fmt.Errorf("unsupported ip probe type %q", g.IPType)
	}
	return addr, nil
}

func (g *Getter) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 10 * time.Second}
}
