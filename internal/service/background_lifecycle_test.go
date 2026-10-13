package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type blockedSchedulerAccounts struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (a *blockedSchedulerAccounts) ListIDs() ([]uint, error) {
	if a.calls.Add(1) == 1 {
		close(a.entered)
	}
	<-a.release
	return nil, nil
}

func TestSLASchedulerDoneWaitsForInFlightOwnerRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	accounts := &blockedSchedulerAccounts{entered: make(chan struct{}), release: make(chan struct{})}
	s := &SLAService{accounts: accounts}
	done := s.StartSLAScheduler(ctx, time.Nanosecond)
	var releaseOnce sync.Once
	t.Cleanup(func() { cancel(); releaseOnce.Do(func() { close(accounts.release) }); <-done })
	select {
	case <-accounts.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not enter owner read")
	}
	cancel()
	select {
	case <-done:
		t.Fatal("scheduler exited before the owner read completed")
	default:
	}
	releaseOnce.Do(func() { close(accounts.release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler did not join completed work")
	}
	if accounts.calls.Load() != 1 {
		t.Fatal("cancelled scheduler began another pass")
	}
}

func TestPeriodicWorkersJoinCancelledLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Nil services deliberately fail if a cancelled worker invokes business work.
	workers := map[string]<-chan struct{}{
		"campaign": (*CampaignService)(nil).StartScheduledCampaignWorker(ctx, time.Nanosecond),
		"sla":      (*SLAService)(nil).StartSLAScheduler(ctx, time.Nanosecond),
		"email":    (*EmailService)(nil).StartEmailRetryWorker(ctx, time.Minute),
		"snooze":   (*RoutingService)(nil).StartSnoozeScheduler(ctx, time.Minute),
	}
	for name, done := range workers {
		t.Run(name, func(t *testing.T) {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled worker did not exit")
			}
		})
	}
}
