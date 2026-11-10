package rss

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/mmcdole/gofeed"
	ext "github.com/mmcdole/gofeed/extensions"
	"golang.org/x/net/html"
)

// imageExts are accepted for media elements without a MIME type or medium
var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".avif": true, ".bmp": true,
}

// itemImages returns every image URL of the item, deduplicated, from the first
// of these tiers that yields any:
//
//  1. media:content (also inside media:group) and image enclosures
//  2. <img> tags in the content, then the description, then gofeed's own pick
//  3. the largest media:thumbnail
//
// Tiers are not merged since feeds often repeat the same image across them in
// other sizes
func itemImages(item *gofeed.Item) []string {
	if item == nil {
		return nil
	}

	c := imageCollector{base: baseURL(item.Link), seen: map[string]bool{}}

	media := item.Extensions["media"]
	c.addMedia(media["content"])
	for _, group := range media["group"] {
		c.addMedia(group.Children["content"])
	}
	for _, enc := range item.Enclosures {
		if enc != nil && strings.HasPrefix(enc.Type, "image/") {
			c.add(enc.URL)
		}
	}

	if len(c.urls) == 0 {
		c.addHTML(item.Content)
		c.addHTML(item.Description)
		// e.g. itunes:image
		if item.Image != nil {
			c.add(item.Image.URL)
		}
	}

	if len(c.urls) == 0 {
		thumbs := media["thumbnail"]
		for _, group := range media["group"] {
			thumbs = append(thumbs, group.Children["thumbnail"]...)
		}
		c.addLargestThumbnail(thumbs)
	}

	return c.urls
}

type imageCollector struct {
	base *url.URL
	seen map[string]bool
	urls []string
}

func baseURL(link string) *url.URL {
	u, err := url.Parse(link)
	if err != nil || !u.IsAbs() {
		return nil
	}
	return u
}

// add resolves raw against the item link and keeps it if it is a new http(s) URL
func (c *imageCollector) add(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	u, err := url.Parse(raw)
	if err != nil {
		return
	}
	if c.base != nil {
		u = c.base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return
	}
	s := u.String()
	if c.seen[s] {
		return
	}
	c.seen[s] = true
	c.urls = append(c.urls, s)
}

func (c *imageCollector) addMedia(contents []ext.Extension) {
	for _, m := range contents {
		if !isImageMedia(m.Attrs) || isPixel(m.Attrs) {
			continue
		}
		c.add(m.Attrs["url"])
	}
}

// addLargestThumbnail adds the thumbnail with the largest declared area, or
// the first one when none declares its size
func (c *imageCollector) addLargestThumbnail(thumbs []ext.Extension) {
	best, bestArea := "", -1
	for _, m := range thumbs {
		if isPixel(m.Attrs) || m.Attrs["url"] == "" {
			continue
		}
		area := dimension(m.Attrs["width"]) * dimension(m.Attrs["height"])
		if area > bestArea {
			best, bestArea = m.Attrs["url"], area
		}
	}
	c.add(best)
}

// dimension parses a width/height attribute like "300" or "300px", 0 if unset
func dimension(v string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func isImageMedia(attrs map[string]string) bool {
	if medium := attrs["medium"]; medium != "" {
		return medium == "image"
	}
	if typ := attrs["type"]; typ != "" {
		return strings.HasPrefix(typ, "image/")
	}
	u, err := url.Parse(attrs["url"])
	if err != nil {
		return false
	}
	return imageExts[strings.ToLower(path.Ext(u.Path))]
}

// isPixel reports whether the element declares a width or height of at most
// 1px, the usual shape of tracking pixels
func isPixel(attrs map[string]string) bool {
	for _, key := range []string{"width", "height"} {
		v, ok := attrs[key]
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), "px"))
		if err == nil && n <= 1 {
			return true
		}
	}
	return false
}

// addHTML collects the src of every <img> in an HTML fragment
func (c *imageCollector) addHTML(fragment string) {
	if !strings.Contains(fragment, "<img") && !strings.Contains(fragment, "<IMG") {
		return
	}
	z := html.NewTokenizer(strings.NewReader(fragment))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			if tok.Data != "img" {
				continue
			}
			attrs := make(map[string]string, len(tok.Attr))
			for _, a := range tok.Attr {
				attrs[a.Key] = a.Val
			}
			if isPixel(attrs) {
				continue
			}
			c.add(attrs["src"])
		}
	}
}
