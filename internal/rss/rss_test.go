package rss

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type recordingSender struct {
	queued []string
	sent   []string
}

func (s *recordingSender) InitQueue(int) { s.queued = nil }

func (s *recordingSender) Enqueue(title string, _ RSSItem) error {
	s.queued = append(s.queued, title)
	return nil
}

func (s *recordingSender) Flush() error {
	s.sent = append(s.sent, s.queued...)
	s.queued = nil
	return nil
}

// serveFeed serves the RSS document returned by body() on every request.
func serveFeed(t *testing.T, body func() string) *url.URL {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(body()))
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func rssDoc(items string) string {
	return `<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>` + items + `</channel></rss>`
}

func TestPubDateJobUndatedItems(t *testing.T) {
	feed := rssDoc(`
<item><title>undated</title><guid>u</guid></item>
<item><title>old</title><guid>o</guid><pubDate>Mon, 02 Jan 2006 15:04:05 GMT</pubDate></item>`)
	u := serveFeed(t, func() string { return feed })

	sender := &recordingSender{}
	job := NewPubDateJob(*slog.Default(), *u, "", false, 5*time.Second, sender)

	ctx := context.Background()
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}

	// all-undated feed must not panic either
	feed = rssDoc(`<item><title>undated</title><guid>u</guid></item>`)
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
}
