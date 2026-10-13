package router

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func awaitBackgroundSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("background lifecycle did not complete")
	}
}

func TestRouterBackgroundStopJoinsInFlightWork(t *testing.T) {
	b := newRouterBackground(nil)
	entered, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	b.add(func(ctx context.Context) {
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-release
		finished.Store(true)
	})
	b.start()
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); b.stop() })
	awaitBackgroundSignal(t, entered)
	stopped := make(chan struct{})
	go func() {
		b.stop()
		if !finished.Load() {
			t.Error("stop returned before in-flight work exited")
		}
		close(stopped)
	}()
	awaitBackgroundSignal(t, cancelled)
	select {
	case <-stopped:
		t.Fatal("stop returned while work was blocked")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	awaitBackgroundSignal(t, stopped)
	// Concurrent and repeated stops must all join the same generation safely.
	var callers sync.WaitGroup
	for i := 0; i < 8; i++ {
		callers.Add(1)
		go func() { defer callers.Done(); b.stop() }()
	}
	callers.Wait()
}

func TestRouterBackgroundParentCancellationAndGenerations(t *testing.T) {
	for generation := 0; generation < 3; generation++ {
		parent, cancel := context.WithCancel(context.Background())
		b := newRouterBackground(parent)
		var exited atomic.Int32
		for i := 0; i < 3; i++ {
			b.add(func(ctx context.Context) { <-ctx.Done(); exited.Add(1) })
		}
		b.start()
		cancel()
		b.stop()
		if exited.Load() != 3 {
			t.Fatalf("generation %d escaped cleanup", generation)
		}
	}
}
