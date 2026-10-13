package router

import (
	"context"
	"sync"
)

// routerBackground owns one fully composed router generation. Registration and
// start are confined to setup; stop may be called concurrently or repeatedly.
type routerBackground struct {
	ctx    context.Context
	cancel context.CancelFunc
	work   []func(context.Context)
	wg     sync.WaitGroup
}

func newRouterBackground(parent context.Context) *routerBackground {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &routerBackground{ctx: ctx, cancel: cancel}
}

func (b *routerBackground) add(work func(context.Context)) { b.work = append(b.work, work) }

func (b *routerBackground) start() {
	b.wg.Add(len(b.work))
	for _, work := range b.work {
		go func(run func(context.Context)) {
			defer b.wg.Done()
			run(b.ctx)
		}(work)
	}
}

func (b *routerBackground) stop() {
	b.cancel()
	b.wg.Wait()
}
