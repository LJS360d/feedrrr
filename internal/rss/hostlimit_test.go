package rss

import (
	"context"
	"testing"
	"time"
)

func TestHostLimit(t *testing.T) {
	SetMaxFetchesPerHost(1)
	defer SetMaxFetchesPerHost(0)

	release, err := acquireHost(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := acquireHost(context.Background(), "b"); err != nil {
		t.Fatal("other host should not wait")
	} else {
		r()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := acquireHost(ctx, "a"); err == nil {
		t.Fatal("second fetch to the same host should wait")
	}
	release()
	if r, err := acquireHost(context.Background(), "a"); err != nil {
		t.Fatal(err)
	} else {
		r()
	}
}
