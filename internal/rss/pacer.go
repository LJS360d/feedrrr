package rss

import (
	"sync"
	"time"
)

// DefaultSendGap is the minimum time between two sends of any job, so jobs
// sharing a schedule don't burst into sink rate limits
const DefaultSendGap = 500 * time.Millisecond

// pacer serializes sends across all jobs and spaces them at least gap apart
type pacer struct {
	mu    sync.Mutex
	gap   time.Duration
	last  time.Time
	sleep func(time.Duration)
}

// sendPacer is shared by every sender
var sendPacer = &pacer{gap: DefaultSendGap, sleep: time.Sleep}

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
