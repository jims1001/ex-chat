package service

import (
	"context"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
)

const DefaultSnoozeInterval = time.Minute

// StartSnoozeScheduler requires an explicit caller-owned lifecycle. Nil-context
// embedded callers stay manually driven; invalid intervals use a safe default.
func (s *RoutingService) StartSnoozeScheduler(ctx context.Context, interval time.Duration) <-chan struct{} {
	done := make(chan struct{})
	if ctx == nil || ctx.Err() != nil {
		close(done)
		return done
	}
	go func() { defer close(done); s.runSnoozeScheduler(ctx, interval) }()
	return done
}

func (s *RoutingService) runSnoozeScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = DefaultSnoozeInterval
	}
	// Run once immediately so restart recovers deadlines that passed offline.
	for ctx.Err() == nil {
		resumed, err := s.ResumeSnoozedConversations(ctx)
		if err != nil && ctx.Err() == nil {
			logger.WithComponent("routing").Warn("expired snooze recovery deferred", "resumed_count", len(resumed))
		}
		// A completion-based delay prevents a slow pass from building a tick
		// backlog. Cancellation also reaches every owner scan and transaction.
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
