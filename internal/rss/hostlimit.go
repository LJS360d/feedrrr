package rss

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrHostBusy means a scheduled fetch found its host busy past its wait limit
var ErrHostBusy = errors.New("host busy (max_fetches_per_host reached), will retry next run")

var (
	hostMu       sync.Mutex
	hostSlots    = map[string]chan struct{}{}
	fetchPerHost int
	// FetchGap spaces the starts of fetches to one host
	FetchGap time.Duration
	hostNext = map[string]time.Time{}
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
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// reserve a start time at least FetchGap after the previous one
	hostMu.Lock()
	start := time.Now()
	if next := hostNext[host]; next.After(start) {
		start = next
	}
	hostNext[host] = start.Add(FetchGap)
	hostMu.Unlock()
	select {
	case <-time.After(time.Until(start)):
		return func() { <-slots }, nil
	case <-ctx.Done():
		<-slots
		return nil, ctx.Err()
	}
}
