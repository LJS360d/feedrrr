package rss

import (
	"testing"
	"time"
)

func init() {
	// tests send through fake routers, no need to wait between sends
	sendPacer.gap = 0
}

func TestPacerSpacesSends(t *testing.T) {
	var slept []time.Duration
	p := &pacer{gap: time.Hour, sleep: func(d time.Duration) { slept = append(slept, d) }}

	sends := 0
	for range 3 {
		p.do(func() { sends++ })
	}

	if sends != 3 {
		t.Fatalf("sends = %d", sends)
	}
	// first send goes out right away, the others wait out the rest of the gap
	if len(slept) != 2 {
		t.Fatalf("slept %v, want 2 waits", slept)
	}
	for _, d := range slept {
		if d <= time.Hour-time.Minute || d > time.Hour {
			t.Errorf("waited %v, want about 1h", d)
		}
	}
}
