package rss

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"time"

	"github.com/mmcdole/gofeed"
)

type FeedPoller interface {
	Init(ctx context.Context) error
	RetrieveAndSendNewItems(ctx context.Context) error
	// State returns what a job needs to resume after a restart
	State() JobState
	// Restore resumes from a saved state, reporting whether it was usable
	Restore(JobState) bool
}

type JobState struct {
	// no omitempty: an empty map (feed was empty when seeded) must restore
	// as such, nil means "not a GUID job state"
	Seen        map[string]time.Time `json:"seen"`
	LastPubDate *time.Time           `json:"last_pub_date,omitempty"`
	Delivered   map[string]bool      `json:"delivered,omitempty"`
}

type RSSJob struct {
	logger      *slog.Logger
	feedURL     *url.URL
	titlePrefix string
	sender      MessageSender
	fp          *gofeed.Parser
	// requireImage skips items without any image; they still count as seen
	requireImage bool
	// hideTitle sends items without a title
	hideTitle bool
	// imageRewrites are applied to every item's image URLs
	imageRewrites []ImageRewrite
	// backfill is how many of the newest items the first run sends
	backfill int
	// fetchTimeout bounds each fetch, and the wait for a slot on its host
	fetchTimeout time.Duration
}

func (j *RSSJob) fetch(ctx context.Context) (*gofeed.Feed, error) {
	waitStart := time.Now()
	waitCtx, cancel := context.WithCancel(ctx)
	if j.fetchTimeout > 0 {
		cancel()
		waitCtx, cancel = context.WithTimeout(ctx, j.fetchTimeout)
	}
	release, err := acquireHost(waitCtx, j.feedURL.Host)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			return nil, fmt.Errorf("no fetch slot for host %s within %s (max_fetches_per_host reached), will retry next run", j.feedURL.Host, j.fetchTimeout)
		}
		return nil, err
	}
	defer release()
	if waited := time.Since(waitStart); waited > time.Second {
		j.logger.Debug("Waited for a fetch slot on host", "host", j.feedURL.Host, "waited", waited.Round(time.Millisecond))
	}
	feed, err := j.fp.ParseURLWithContext(j.feedURL.String(), ctx)
	if err != nil {
		return nil, err
	}
	j.logger.Debug("Got feed", "amount", len(feed.Items))
	return feed, nil
}

// send delivers items and returns the keys of those not delivered (all of
// them on a render error)
func (j *RSSJob) send(items []*gofeed.Item) (failed map[string]bool, err error) {
	if len(items) == 0 {
		return nil, nil
	}

	for idx, item := range items {
		rssItem := RSSItemFromGoFeedItem(item)
		rssItem.Images = rewriteImages(j.imageRewrites, rssItem.Images)
		if len(rssItem.Images) > 0 {
			rssItem.Image = rssItem.Images[0]
		}
		if j.requireImage && len(rssItem.Images) == 0 {
			j.logger.Debug("Skip item without image", "item", item, "idx", idx)
			continue
		}

		title := ""
		if !j.hideTitle {
			title = j.titlePrefix + item.Title
		}
		j.logger.Debug("New item", "item", item, "idx", idx)
		if err := j.sender.Enqueue(title, *rssItem); err != nil {
			failed := make(map[string]bool, len(items))
			for _, item := range items {
				failed[itemKey(item)] = true
			}
			return failed, err
		}
	}

	failedItems, err := j.sender.Flush()
	return itemKeys(failedItems), err
}

// seenRetention is how long an item key is remembered after it was last in
// the feed, so items that drop out and come back aren't resent
const seenRetention = 7 * 24 * time.Hour

// seenRefresh is how stale a key's last-seen time may get before it's
// updated; refreshing on every run would change (and rewrite) the saved
// state each time. Keys still in the feed stay far inside seenRetention
const seenRefresh = 24 * time.Hour

// GUIDJob sends items whose key wasn't seen within seenRetention
type GUIDJob struct {
	RSSJob
	seen map[string]time.Time // item key -> last time it was in the feed
	now  func() time.Time
}

func newFeedParser(fetchTimeout time.Duration) *gofeed.Parser {
	fp := gofeed.NewParser()
	fp.Client = &http.Client{Timeout: fetchTimeout}
	return fp
}

// itemKey identifies an item: GUID, falling back to link, then title.
func itemKey(item *gofeed.Item) string {
	if item == nil {
		return ""
	}
	return keyOf(item.GUID, item.Link, item.Title)
}

func keyOf(guid, link, title string) string {
	if guid != "" {
		return guid
	}
	if link != "" {
		return link
	}
	return title
}

// key matches itemKey of the source feed item
func (i RSSItem) key() string {
	return keyOf(i.GUID, i.Link, i.Title)
}

func itemKeys(items []RSSItem) map[string]bool {
	keys := make(map[string]bool, len(items))
	for _, item := range items {
		keys[item.key()] = true
	}
	return keys
}

// markSeen records every item in the feed as seen now, except those in skip,
// and forgets keys that have been absent for longer than seenRetention
func (j *GUIDJob) markSeen(items []*gofeed.Item, skip map[string]bool) {
	now := j.now()
	if j.seen == nil {
		j.seen = make(map[string]time.Time, len(items))
	}
	for _, item := range items {
		if item == nil || skip[itemKey(item)] {
			continue
		}
		key := itemKey(item)
		if last, ok := j.seen[key]; !ok || now.Sub(last) >= seenRefresh {
			j.seen[key] = now
		}
	}
	for key, last := range j.seen {
		if now.Sub(last) > seenRetention {
			delete(j.seen, key)
		}
	}
}

// itemTime returns the item's publish time, falling back to its update time;
// nil when the feed provides neither (or the item is nil)
func itemTime(item *gofeed.Item) *time.Time {
	if item == nil {
		return nil
	}
	if item.PublishedParsed != nil {
		return item.PublishedParsed
	}
	return item.UpdatedParsed
}

type PubDateJob struct {
	RSSJob
	lastPubDate     *time.Time
	sendFutureItems bool
	// delivered holds keys sent by a partly failed run, so retrying it
	// doesn't resend them
	delivered map[string]bool
}

func (j *GUIDJob) Init(ctx context.Context) error {
	feed, err := j.fetch(ctx)
	if err != nil {
		return err
	}

	j.seed(feed.Items)
	j.logger.Debug("Init", "seen", len(j.seen))
	return nil
}

// seed records the initial feed as seen, except the first backfill items
// (feeds list newest first), which the next run then sends
func (j *GUIDJob) seed(items []*gofeed.Item) {
	skip := make(map[string]bool, j.backfill)
	for _, item := range items {
		if len(skip) >= j.backfill {
			break
		}
		if item != nil {
			skip[itemKey(item)] = true
		}
	}
	j.markSeen(items, skip)
}

func (j *GUIDJob) RetrieveAndSendNewItems(ctx context.Context) error {
	feed, err := j.fetch(ctx)
	if err != nil {
		return err
	}

	// special case: the initial fetch failed, so seed state without sending
	if j.seen == nil {
		j.seed(feed.Items)
		j.logger.Info("Seeded state on first successful fetch", "items", len(j.seen))
		return nil
	}

	var items []*gofeed.Item
	for _, item := range feed.Items {
		if item == nil {
			continue
		}
		if _, ok := j.seen[itemKey(item)]; !ok {
			items = append(items, item)
		}
	}

	if len(items) > 0 {
		j.logger.Info("Found new items", "new", len(items), "in_feed", len(feed.Items))
	}
	// items that failed to send stay unseen so the next run retries just them
	failed, err := j.send(items)
	j.markSeen(feed.Items, failed)
	if err != nil {
		j.logger.Warn("Some items failed to send, they'll be retried next run", "failed", len(failed), "new", len(items))
		return err
	}

	j.logger.Debug("Successful iteration")
	return nil
}

func (j *GUIDJob) State() JobState {
	return JobState{Seen: maps.Clone(j.seen)}
}

func (j *GUIDJob) Restore(s JobState) bool {
	if s.Seen == nil {
		return false
	}
	j.seen = s.Seen
	return true
}

func (j *PubDateJob) State() JobState {
	t := *j.lastPubDate
	return JobState{LastPubDate: &t, Delivered: maps.Clone(j.delivered)}
}

func (j *PubDateJob) Restore(s JobState) bool {
	if s.LastPubDate == nil {
		return false
	}
	*j.lastPubDate = *s.LastPubDate
	j.delivered = s.Delivered
	return true
}

func (j *PubDateJob) Init(ctx context.Context) error {
	defer j.logger.Debug("Init", "lastPubDate", *j.lastPubDate)

	feed, err := j.fetch(ctx)
	if err != nil {
		return err
	}

	// cut at the newest dated item, or backfill dated items below it
	var cut *time.Time
	remaining := j.backfill
	for _, item := range feed.Items {
		t := itemTime(item)
		if t == nil {
			continue
		}
		if remaining == 0 {
			cut = t
			break
		}
		remaining--
	}
	switch {
	case cut != nil:
		*j.lastPubDate = *cut
	case j.backfill > 0:
		*j.lastPubDate = time.Time{}
	}
	// no dated items and no backfill: keep the creation time as the cut
	return nil
}

func (j *PubDateJob) RetrieveAndSendNewItems(ctx context.Context) error {
	feed, err := j.fetch(ctx)
	if err != nil {
		return err
	}
	if len(feed.Items) == 0 {
		return nil
	}

	now := time.Now()
	currentTopPubDate := *j.lastPubDate
	for _, item := range feed.Items {
		if t := itemTime(item); t != nil {
			currentTopPubDate = *t
			break
		}
	}
	if !j.sendFutureItems && currentTopPubDate.After(now) {
		currentTopPubDate = now
	}

	var items []*gofeed.Item
	for idx, item := range feed.Items {
		pubDate := itemTime(item)
		if pubDate == nil {
			j.logger.Debug("Skip item without date", "item", item, "idx", idx)
			continue
		}
		// assumption: items are sorted by pubdate; lastPubDate itself was already sent
		if !pubDate.After(*j.lastPubDate) {
			j.logger.Debug("Break", "item", item, "lastPubDate", *j.lastPubDate, "idx", idx)
			break
		}
		if !j.sendFutureItems && pubDate.After(now) {
			j.logger.Debug("Continue", "item", item, "lastPubDate", *j.lastPubDate, "idx", idx)
			continue
		}
		if j.delivered[itemKey(item)] {
			j.logger.Debug("Skip item delivered by a failed run", "item", item, "idx", idx)
			continue
		}

		items = append(items, item)
	}

	if len(items) > 0 {
		j.logger.Info("Found new items", "new", len(items), "in_feed", len(feed.Items), "since", j.lastPubDate.Format(time.RFC3339))
	}
	failed, err := j.send(items)
	if err != nil {
		j.logger.Warn("Some items failed to send, the run will be retried without resending the others", "failed", len(failed), "new", len(items))
		for _, item := range items {
			if key := itemKey(item); !failed[key] {
				if j.delivered == nil {
					j.delivered = make(map[string]bool)
				}
				j.delivered[key] = true
			}
		}
		return err
	}

	j.logger.Debug("Successful iteration")
	*j.lastPubDate = currentTopPubDate
	j.delivered = nil
	return nil
}

// JobOptions are the settings shared by every kind of job
type JobOptions struct {
	// TitlePrefix is prepended to every item title
	TitlePrefix string
	// RequireImage skips items without any image
	RequireImage bool
	// HideTitle sends items without a title, leaving just the rendered template
	HideTitle bool
	// FetchTimeout bounds each feed fetch
	FetchTimeout time.Duration
	// ImageRewrites rewrite item image URLs
	ImageRewrites []ImageRewrite
	// Backfill sends this many of the newest items on the first run
	Backfill int
}

func newRSSJob(logger slog.Logger, url url.URL, kind string, opts JobOptions, sender MessageSender) RSSJob {
	return RSSJob{
		logger:        logger.With("feedURL", url.String(), "type", kind),
		feedURL:       &url,
		titlePrefix:   opts.TitlePrefix,
		sender:        sender,
		fp:            newFeedParser(opts.FetchTimeout),
		requireImage:  opts.RequireImage,
		hideTitle:     opts.HideTitle,
		backfill:      max(opts.Backfill, 0),
		fetchTimeout:  opts.FetchTimeout,
		imageRewrites: opts.ImageRewrites,
	}
}

func NewGUIDJob(logger slog.Logger, url url.URL, opts JobOptions, sender MessageSender) FeedPoller {
	logger.Debug("Creating new GUID job")
	return &GUIDJob{
		newRSSJob(logger, url, "guid", opts, sender),
		nil,
		time.Now,
	}
}

func NewPubDateJob(logger slog.Logger, url url.URL, opts JobOptions, sendFutureItems bool, sender MessageSender) FeedPoller {
	lastPubDate := time.Now()
	logger.Debug("Creating new PubDate job")
	return &PubDateJob{
		newRSSJob(logger, url, "pubdate", opts, sender),
		&lastPubDate,
		sendFutureItems,
		nil,
	}
}
