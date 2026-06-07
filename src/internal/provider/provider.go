package provider

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

var (
	ErrRecordNotFound  = errors.New("dns record not found")
	ErrMultipleRecords = errors.New("multiple dns records matched")
)

type TargetRecord struct {
	Provider     string `json:"provider"`
	Zone         string `json:"zone"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	TTL          int    `json:"ttl"`
	Proxied      bool   `json:"proxied"`
	RecordLine   string `json:"record_line"`
	RecordLineID string `json:"record_line_id"`
}

func (r TargetRecord) Key() string {
	return strings.Join([]string{
		strings.ToLower(r.Provider),
		strings.ToLower(r.Zone),
		strings.ToLower(r.Name),
		strings.ToUpper(r.Type),
	}, "|")
}

type DNSRecord struct {
	ID           string
	Zone         string
	Name         string
	Type         string
	Value        netip.Addr
	TTL          int
	Proxied      bool
	RecordLine   string
	RecordLineID string
}

type Provider interface {
	GetARecord(ctx context.Context, target TargetRecord) (DNSRecord, error)
	UpdateARecord(ctx context.Context, record DNSRecord, value netip.Addr, ttl int) error
}

type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

func (r *Registry) Register(name string, p Provider) {
	r.providers[strings.ToLower(name)] = p
}

func (r *Registry) Get(name string) (Provider, error) {
	if r == nil {
		return nil, fmt.Errorf("provider registry is nil")
	}
	p, ok := r.providers[strings.ToLower(name)]
	if !ok {
		return nil, fmt.Errorf("provider %q is not configured", name)
	}
	return p, nil
}
