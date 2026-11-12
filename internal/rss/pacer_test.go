package rss

import (
	"testing"
	"time"
)

func init() {
	// tests send through fake routers, no need to wait between sends
	sendGap = 0
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

func TestPacersPerSink(t *testing.T) {
	a := pacersFor([]string{"discord://t@1?color=1"})
	b := pacersFor([]string{"discord://t@1?color=2"})
	c := pacersFor([]string{"discord://t@2", "discord://t@1"})
	if a[0] != b[0] {
		t.Error("sinks differing only in query should share a pacer")
	}
	if len(c) != 2 || (c[0] != a[0] && c[1] != a[0]) {
		t.Errorf("multi-sink group %v should include the shared pacer", c)
	}
	if pacersFor([]string{"discord://t@3"})[0] == a[0] {
		t.Error("different sinks should not share a pacer")
	}
}
