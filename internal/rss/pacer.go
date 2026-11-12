package rss

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultSendGap is the minimum time between two sends to the same sink, so jobs
// sharing a schedule don't burst into sink rate limits
const DefaultSendGap = 1000 * time.Millisecond

// pacer serializes sends to one sink and spaces them at least gap apart
type pacer struct {
	mu    sync.Mutex
	gap   time.Duration
	last  time.Time
	sleep func(time.Duration)
}

var (
	pacersMu sync.Mutex
	pacers   = map[string]*pacer{}
	sendGap  = DefaultSendGap
)

// SetSendGap changes the minimum time between two sends; gap <= 0 keeps the current one.
func SetSendGap(gap time.Duration) {
	if gap <= 0 {
		return
	}
	pacersMu.Lock()
	defer pacersMu.Unlock()
	sendGap = gap
	for _, p := range pacers {
		p.mu.Lock()
		p.gap = gap
		p.mu.Unlock()
	}
}

// pacersFor returns the shared pacers of the given sink URLs; sinks differing
// only in their query parameters share one
func pacersFor(sinks []string) pacerGroup {
	keys := make([]string, 0, len(sinks))
	for _, s := range sinks {
		s, _, _ = strings.Cut(s, "?")
		keys = append(keys, s)
	}
	if len(keys) == 0 {
		keys = append(keys, "")
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)

	pacersMu.Lock()
	defer pacersMu.Unlock()
	group := make(pacerGroup, len(keys))
	for i, k := range keys {
		if pacers[k] == nil {
			pacers[k] = &pacer{gap: sendGap, sleep: time.Sleep}
		}
		group[i] = pacers[k]
	}
	return group
}

// do runs send at least gap after the previous send ended
func (p *pacer) do(send func()) {
	pacerGroup{p}.do(send)
}

// pacerGroup paces a send that goes to several sinks at once
type pacerGroup []*pacer

func (g pacerGroup) do(send func()) {
	for _, p := range g {
		p.mu.Lock()
		defer p.mu.Unlock()
	}

	var wait time.Duration
	for _, p := range g {
		if !p.last.IsZero() {
			wait = max(wait, p.gap-time.Since(p.last))
		}
	}
	if wait > 0 {
		g[0].sleep(wait)
	}
	send()
	now := time.Now()
	for _, p := range g {
		p.last = now
	}
}
