package rss

import (
	"time"

	"github.com/mmcdole/gofeed"
)

type RSSItem struct {
	Title       string
	PubDate     time.Time
	Content     string
	Description string
	GUID        string
	Link        string
}

func RSSItemFromGoFeedItem(item *gofeed.Item) *RSSItem {
	// Not every feed item carries a parseable date; never deref nil
	var pubDate time.Time
	if t := itemTime(item); t != nil {
		pubDate = *t
	}

	return &RSSItem{
		Title:       item.Title,
		PubDate:     pubDate,
		Content:     item.Content,
		Description: item.Description,
		GUID:        item.GUID,
		Link:        item.Link,
	}
}
