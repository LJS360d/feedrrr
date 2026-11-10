package rss

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"testing"
	"time"
)

// recordingSender records the titles it delivers; titles in fail are not
// delivered and reported as failed
type recordingSender struct {
	queued    []RSSItem
	sent      []string
	sentItems []RSSItem
	fail      map[string]bool
}

func (s *recordingSender) Enqueue(title string, item RSSItem) error {
	item.Title = title
	s.queued = append(s.queued, item)
	return nil
}

func (s *recordingSender) Flush() ([]RSSItem, error) {
	var failed []RSSItem
	for _, item := range s.queued {
		if s.fail[item.Title] {
			failed = append(failed, item)
			continue
		}
		s.sent = append(s.sent, item.Title)
		s.sentItems = append(s.sentItems, item)
	}
	s.queued = nil
	if len(failed) > 0 {
		return failed, errors.New("send failed")
	}
	return nil, nil
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
	job := NewPubDateJob(*slog.Default(), *u, JobOptions{RequireImage: false, FetchTimeout: 5 * time.Second}, false, sender)

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

func TestPubDateJobDoesNotResendSeenItems(t *testing.T) {
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC1123Z)
	newer := time.Now().Add(-time.Minute).UTC().Format(time.RFC1123Z)
	feed := rssDoc(`<item><title>a</title><guid>a</guid><pubDate>` + recent + `</pubDate></item>`)
	u := serveFeed(t, func() string { return feed })

	sender := &recordingSender{}
	job := NewPubDateJob(*slog.Default(), *u, JobOptions{RequireImage: false, FetchTimeout: 5 * time.Second}, false, sender)

	ctx := context.Background()
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := job.RetrieveAndSendNewItems(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.sent) != 0 {
		t.Fatalf("unchanged feed sent %v, want nothing", sender.sent)
	}

	feed = rssDoc(`<item><title>b</title><guid>b</guid><pubDate>` + newer + `</pubDate></item>` +
		`<item><title>a</title><guid>a</guid><pubDate>` + recent + `</pubDate></item>`)
	for range 2 {
		if err := job.RetrieveAndSendNewItems(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(sender.sent) != 1 || sender.sent[0] != "b" {
		t.Fatalf("sent %v, want [b]", sender.sent)
	}
}

func TestGUIDJobDoesNotResendReappearingItems(t *testing.T) {
	feed := rssDoc(`<item><title>a</title><guid>a</guid></item><item><title>b</title><guid>b</guid></item>`)
	u := serveFeed(t, func() string { return feed })

	sender := &recordingSender{}
	job := NewGUIDJob(*slog.Default(), *u, JobOptions{RequireImage: false, FetchTimeout: 5 * time.Second}, sender).(*GUIDJob)
	clock := time.Now()
	job.now = func() time.Time { return clock }

	ctx := context.Background()
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}

	// a drops out of the ranking, then comes back
	feed = rssDoc(`<item><title>b</title><guid>b</guid></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Hour)
	feed = rssDoc(`<item><title>a</title><guid>a</guid></item><item><title>c</title><guid>c</guid></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != "c" {
		t.Fatalf("sent %v, want [c]", sender.sent)
	}

	// keys absent for longer than the retention are forgotten
	clock = clock.Add(seenRetention + time.Hour)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := job.seen["b"]; ok {
		t.Fatal("b should have been pruned")
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %v, want [c]", sender.sent)
	}
}

func TestRequireImageSkipsItemsWithoutImages(t *testing.T) {
	feed := rssDoc(``)
	u := serveFeed(t, func() string { return feed })

	ctx := context.Background()
	sender := &recordingSender{}
	job := NewGUIDJob(*slog.Default(), *u, JobOptions{RequireImage: true, FetchTimeout: 5 * time.Second}, sender)
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}

	feed = rssDoc(`
<item><title>text</title><guid>t</guid><description>no picture</description></item>
<item><title>pic</title><guid>p</guid><description>&lt;img src="https://i.example/p.jpg"&gt;</description></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != "pic" {
		t.Fatalf("sent %v, want [pic]", sender.sent)
	}

	// the skipped item is seen: gaining an image later does not send it
	feed = rssDoc(`<item><title>text</title><guid>t</guid><description>&lt;img src="https://i.example/t.jpg"&gt;</description></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 {
		t.Fatalf("sent %v, want [pic]", sender.sent)
	}
}

func TestRequireImagePubDate(t *testing.T) {
	recent := time.Now().Add(time.Minute).UTC().Format(time.RFC1123Z)
	feed := rssDoc(``)
	u := serveFeed(t, func() string { return feed })

	ctx := context.Background()
	sender := &recordingSender{}
	job := NewPubDateJob(*slog.Default(), *u, JobOptions{RequireImage: true, FetchTimeout: 5 * time.Second}, true, sender)
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}

	feed = rssDoc(`
<item><title>text</title><guid>t</guid><pubDate>` + recent + `</pubDate></item>
<item><title>pic</title><guid>p</guid><pubDate>` + recent + `</pubDate><enclosure url="https://i.example/p.jpg" type="image/jpeg" length="1"/></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != "pic" {
		t.Fatalf("sent %v, want [pic]", sender.sent)
	}
}

func TestGUIDJobRetriesOnlyFailedItems(t *testing.T) {
	feed := rssDoc(``)
	u := serveFeed(t, func() string { return feed })

	ctx := context.Background()
	sender := &recordingSender{fail: map[string]bool{"b": true}}
	job := NewGUIDJob(*slog.Default(), *u, JobOptions{RequireImage: false, FetchTimeout: 5 * time.Second}, sender)
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}

	feed = rssDoc(`<item><title>a</title><guid>a</guid></item><item><title>b</title><guid>b</guid></item><item><title>c</title><guid>c</guid></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err == nil {
		t.Fatal("want error from failed send")
	}
	if !reflect.DeepEqual(sender.sent, []string{"a", "c"}) {
		t.Fatalf("sent %v, want [a c]", sender.sent)
	}

	sender.fail = nil
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.sent, []string{"a", "c", "b"}) {
		t.Fatalf("sent %v, want [a c b]", sender.sent)
	}
}

func TestPubDateJobRetriesOnlyFailedItems(t *testing.T) {
	feed := rssDoc(``)
	u := serveFeed(t, func() string { return feed })

	ctx := context.Background()
	sender := &recordingSender{fail: map[string]bool{"b": true}}
	job := NewPubDateJob(*slog.Default(), *u, JobOptions{RequireImage: false, FetchTimeout: 5 * time.Second}, true, sender).(*PubDateJob)
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}
	start := *job.lastPubDate

	date := func(d time.Duration) string { return start.Add(d).UTC().Format(time.RFC1123Z) }
	feed = rssDoc(`
<item><title>c</title><guid>c</guid><pubDate>` + date(3*time.Minute) + `</pubDate></item>
<item><title>b</title><guid>b</guid><pubDate>` + date(2*time.Minute) + `</pubDate></item>
<item><title>a</title><guid>a</guid><pubDate>` + date(time.Minute) + `</pubDate></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err == nil {
		t.Fatal("want error from failed send")
	}
	if !reflect.DeepEqual(sender.sent, []string{"c", "a"}) {
		t.Fatalf("sent %v, want [c a]", sender.sent)
	}
	if !job.lastPubDate.Equal(start) {
		t.Fatal("lastPubDate advanced despite a failed item")
	}

	sender.fail = nil
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.sent, []string{"c", "a", "b"}) {
		t.Fatalf("sent %v, want [c a b]", sender.sent)
	}
	if job.delivered != nil {
		t.Fatal("delivered not cleared after a successful run")
	}

	// nothing new: nothing sent
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("sent %v", sender.sent)
	}
}

func TestHideTitle(t *testing.T) {
	feed := rssDoc(``)
	u := serveFeed(t, func() string { return feed })

	ctx := context.Background()
	sender := &recordingSender{}
	job := NewGUIDJob(*slog.Default(), *u, JobOptions{TitlePrefix: "[x] ", HideTitle: true, FetchTimeout: 5 * time.Second}, sender)
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}

	feed = rssDoc(`<item><title>a</title><guid>a</guid></item>`)
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sender.sent, []string{""}) {
		t.Fatalf("sent %q, want one untitled item", sender.sent)
	}
}

func TestGUIDJobBackfill(t *testing.T) {
	feed := rssDoc(`<item><title>c</title><guid>c</guid></item><item><title>b</title><guid>b</guid></item><item><title>a</title><guid>a</guid></item>`)
	u := serveFeed(t, func() string { return feed })

	sender := &recordingSender{}
	job := NewGUIDJob(*slog.Default(), *u, JobOptions{Backfill: 2, FetchTimeout: 5 * time.Second}, sender)

	ctx := context.Background()
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := job.RetrieveAndSendNewItems(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"c", "b"}; !reflect.DeepEqual(sender.sent, want) {
		t.Fatalf("sent %v, want %v", sender.sent, want)
	}
}

func TestPubDateJobBackfill(t *testing.T) {
	feed := rssDoc(`<item><title>c</title><guid>c</guid><pubDate>Wed, 03 Jan 2024 00:00:00 GMT</pubDate></item>` +
		`<item><title>undated</title><guid>u</guid></item>` +
		`<item><title>b</title><guid>b</guid><pubDate>Tue, 02 Jan 2024 00:00:00 GMT</pubDate></item>` +
		`<item><title>a</title><guid>a</guid><pubDate>Mon, 01 Jan 2024 00:00:00 GMT</pubDate></item>`)
	u := serveFeed(t, func() string { return feed })

	for _, tc := range []struct {
		backfill int
		want     []string
	}{
		{0, nil},
		{2, []string{"c", "b"}},
		{5, []string{"c", "b", "a"}},
	} {
		sender := &recordingSender{}
		job := NewPubDateJob(*slog.Default(), *u, JobOptions{Backfill: tc.backfill, FetchTimeout: 5 * time.Second}, false, sender)
		ctx := context.Background()
		if err := job.Init(ctx); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := job.RetrieveAndSendNewItems(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(sender.sent, tc.want) {
			t.Fatalf("backfill %d: sent %v, want %v", tc.backfill, sender.sent, tc.want)
		}
	}
}

func TestJobAppliesImageRewrites(t *testing.T) {
	feed := rssDoc(`<item><title>a</title><guid>a</guid><description><![CDATA[<img src="https://i/img-original/a_p0.png">]]></description></item>`)
	u := serveFeed(t, func() string { return feed })

	sender := &recordingSender{}
	rules := []ImageRewrite{{Match: regexp.MustCompile(`/img-original/(.+)\.png$`), Replace: "/img-master/${1}_master1200.jpg"}}
	job := NewGUIDJob(*slog.Default(), *u, JobOptions{Backfill: 1, ImageRewrites: rules, FetchTimeout: 5 * time.Second}, sender)
	ctx := context.Background()
	if err := job.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := job.RetrieveAndSendNewItems(ctx); err != nil {
		t.Fatal(err)
	}
	want := "https://i/img-master/a_p0_master1200.jpg"
	if len(sender.queued) != 0 || len(sender.sentItems) != 1 || sender.sentItems[0].Image != want || !reflect.DeepEqual(sender.sentItems[0].Images, []string{want}) {
		t.Fatalf("sent %+v, want image %s", sender.sentItems, want)
	}
}
