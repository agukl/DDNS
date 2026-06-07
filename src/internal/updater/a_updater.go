package updater

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"ddns/internal/provider"
	"ddns/internal/state"
)

type AUpdater struct {
	Providers *provider.Registry
	State     *state.Store
	Logger    *slog.Logger
}

type UpdateResult struct {
	Changed bool
	OldIP   string
	NewIP   string
}

func (u *AUpdater) EnsureA(ctx context.Context, target provider.TargetRecord, current provider.DNSRecord, desired netip.Addr) (UpdateResult, error) {
	if !desired.Is4() {
		return UpdateResult{}, fmt.Errorf("desired ip %s is not an ipv4 address", desired)
	}

	result := UpdateResult{
		Changed: false,
		OldIP:   current.Value.String(),
		NewIP:   desired.String(),
	}
	logger := u.logger().With(
		"provider", target.Provider,
		"record", target.Name,
		"type", target.Type,
	)

	if current.Value == desired {
		logger.Info("a record already up to date", "ip", desired.String())
		return result, nil
	}

	p, err := u.Providers.Get(target.Provider)
	if err != nil {
		return result, err
	}

	current.Proxied = target.Proxied
	if err := p.UpdateARecord(ctx, current, desired, target.TTL); err != nil {
		return result, err
	}
	if u.State != nil {
		if err := u.State.MarkSuccess(target, desired, time.Now()); err != nil {
			return result, fmt.Errorf("write state after update: %w", err)
		}
	}

	result.Changed = true
	logger.Info("a record updated", "old_ip", result.OldIP, "new_ip", result.NewIP)
	return result, nil
}

func (u *AUpdater) logger() *slog.Logger {
	if u != nil && u.Logger != nil {
		return u.Logger
	}
	return slog.Default()
}
