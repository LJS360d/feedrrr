package rss

import (
	"context"
	"sync"
)

var (
	hostMu       sync.Mutex
	hostSlots    = map[string]chan struct{}{}
	fetchPerHost int
)

// SetMaxFetchesPerHost caps concurrent feed fetches to one host; n <= 0 means no limit
func SetMaxFetchesPerHost(n int) {
	hostMu.Lock()
	defer hostMu.Unlock()
	fetchPerHost = n
	hostSlots = map[string]chan struct{}{}
}

// acquireHost waits for a fetch slot for host and returns its release
func acquireHost(ctx context.Context, host string) (func(), error) {
	hostMu.Lock()
	if fetchPerHost <= 0 {
		hostMu.Unlock()
		return func() {}, nil
	}
	slots := hostSlots[host]
	if slots == nil {
		slots = make(chan struct{}, fetchPerHost)
		hostSlots[host] = slots
	}
	hostMu.Unlock()

	select {
	case slots <- struct{}{}:
		return func() { <-slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
