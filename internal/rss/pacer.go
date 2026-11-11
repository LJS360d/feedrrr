package rss

import (
	"sync"
	"time"
)

// DefaultSendGap is the minimum time between two sends of any job, so jobs
// sharing a schedule don't burst into sink rate limits
const DefaultSendGap = 1000 * time.Millisecond

// pacer serializes sends across all jobs and spaces them at least gap apart
type pacer struct {
	mu    sync.Mutex
	gap   time.Duration
	last  time.Time
	sleep func(time.Duration)
}

// sendPacer is shared by every sender
var sendPacer = &pacer{gap: DefaultSendGap, sleep: time.Sleep}

// SetSendGap changes the minimum time between two sends; gap <= 0 keeps the current one.
func SetSendGap(gap time.Duration) {
	if gap <= 0 {
		return
	}
	sendPacer.mu.Lock()
	defer sendPacer.mu.Unlock()
	sendPacer.gap = gap
}

// do runs send at least gap after the previous send ended
func (p *pacer) do(send func()) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if wait := p.gap - time.Since(p.last); wait > 0 && !p.last.IsZero() {
		p.sleep(wait)
	}
	send()
	p.last = time.Now()
}
