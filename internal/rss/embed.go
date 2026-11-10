package rss

import (
	"log/slog"

	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// MaxEmbeds caps the images sent per item
const MaxEmbeds = 4

// EmbedCollector backs the `embed` template func: it records the image URLs
// requested while rendering one item
type EmbedCollector struct {
	// inline renders the URL as text, for plain-text senders
	inline bool
	warned bool
	urls   []string
}

func NewEmbedCollector(inline bool) *EmbedCollector {
	return &EmbedCollector{inline: inline}
}

// Embed is the `embed` template func. Empty URLs are ignored
func (c *EmbedCollector) Embed(url string) string {
	if url == "" {
		return ""
	}
	if len(c.urls) >= MaxEmbeds {
		slog.Debug("Ignoring embed past the limit", "url", url, "max", MaxEmbeds)
		return ""
	}
	c.urls = append(c.urls, url)

	if c.inline {
		if !c.warned {
			slog.Warn("embed is not supported in batched sender mode, rendering image URLs as text")
			c.warned = true
		}
		return url + "\n"
	}
	return ""
}

// reset forgets the URLs of the previous render
func (c *EmbedCollector) reset() {
	if c != nil {
		c.urls = nil
	}
}

// take returns the collected URLs; nil when rendered inline
func (c *EmbedCollector) take() []string {
	if c == nil {
		return nil
	}
	urls := c.urls
	c.urls = nil
	if c.inline {
		return nil
	}
	return urls
}

// embedItems builds one item per image, the first also carrying the text
func embedItems(msg string, images []string) []types.MessageItem {
	items := make([]types.MessageItem, 0, len(images))
	for i, img := range images {
		item := types.MessageItem{}
		if i == 0 {
			item.Text = msg
		}
		item.WithField("embed_image_url", img)
		items = append(items, item)
	}
	return items
}
