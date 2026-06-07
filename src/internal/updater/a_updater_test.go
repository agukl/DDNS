package updater_test

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"path/filepath"
	"testing"

	"ddns/internal/provider"
	"ddns/internal/state"
	"ddns/internal/updater"
)

func TestEnsureASkipsWhenIPMatches(t *testing.T) {
	fake := &fakeProvider{}
	registry := provider.NewRegistry()
	registry.Register("cloudflare", fake)

	ip := netip.MustParseAddr("1.2.3.4")
	target := targetRecord()
	current := provider.DNSRecord{
		ID:    "record-id",
		Zone:  target.Zone,
		Name:  target.Name,
		Type:  "A",
		Value: ip,
	}

	up := &updater.AUpdater{
		Providers: registry,
		Logger:    discardLogger(),
	}
	result, err := up.EnsureA(context.Background(), target, current, ip)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed {
		t.Fatal("Changed = true, want false")
	}
	if fake.updates != 0 {
		t.Fatalf("updates = %d, want 0", fake.updates)
	}
}

func TestEnsureAUpdatesAndWritesState(t *testing.T) {
	fake := &fakeProvider{}
	registry := provider.NewRegistry()
	registry.Register("cloudflare", fake)

	target := targetRecord()
	current := provider.DNSRecord{
		ID:    "record-id",
		Zone:  target.Zone,
		Name:  target.Name,
		Type:  "A",
		Value: netip.MustParseAddr("1.2.3.4"),
	}
	desired := netip.MustParseAddr("1.2.3.5")
	store := state.New(filepath.Join(t.TempDir(), "state.json"))

	up := &updater.AUpdater{
		Providers: registry,
		State:     store,
		Logger:    discardLogger(),
	}
	result, err := up.EnsureA(context.Background(), target, current, desired)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("Changed = false, want true")
	}
	if fake.updates != 1 {
		t.Fatalf("updates = %d, want 1", fake.updates)
	}
	if fake.value != desired {
		t.Fatalf("updated value = %s, want %s", fake.value, desired)
	}

	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Records[target.Key()].LastIP; got != desired.String() {
		t.Fatalf("state last ip = %q, want %q", got, desired.String())
	}
}

type fakeProvider struct {
	updates int
	value   netip.Addr
}

func (f *fakeProvider) GetARecord(context.Context, provider.TargetRecord) (provider.DNSRecord, error) {
	return provider.DNSRecord{}, nil
}

func (f *fakeProvider) UpdateARecord(_ context.Context, _ provider.DNSRecord, value netip.Addr, _ int) error {
	f.updates++
	f.value = value
	return nil
}

func targetRecord() provider.TargetRecord {
	return provider.TargetRecord{
		Provider: "cloudflare",
		Zone:     "example.com",
		Name:     "home.example.com",
		Type:     "A",
		TTL:      300,
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
