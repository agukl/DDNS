package poller

import (
	"context"
	"log/slog"
	"time"
)

type Poller struct {
	Interval time.Duration
	Sync     func(ctx context.Context) error
	Logger   *slog.Logger
}

func (p *Poller) RunOnce(ctx context.Context) error {
	return p.Sync(ctx)
}

func (p *Poller) RunLoop(ctx context.Context) error {
	interval := p.Interval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	for {
		if err := p.RunOnce(ctx); err != nil {
			p.logger().Error("sync round failed", "error", err)
		}

		p.logger().Info("next check scheduled", "after", interval.String(), "next_at", time.Now().Add(interval).Format(time.RFC3339))
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			p.logger().Info("poller stopped")
			return nil
		case <-timer.C:
		}
	}
}

func (p *Poller) logger() *slog.Logger {
	if p != nil && p.Logger != nil {
		return p.Logger
	}
	return slog.Default()
}
