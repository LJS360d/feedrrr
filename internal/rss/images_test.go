package rss

import (
	"reflect"
	"testing"

	"github.com/mmcdole/gofeed"
)

func parseItems(t *testing.T, doc string) []*gofeed.Item {
	t.Helper()
	feed, err := gofeed.NewParser().ParseString(doc)
	if err != nil {
		t.Fatal(err)
	}
	return feed.Items
}

func TestItemImages(t *testing.T) {
	tests := []struct {
		name string
		feed string
		want []string
	}{
		{
			name: "deviantart media:content wins over description copy and thumbnails",
			feed: `<?xml version="1.0"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel><title>t</title>
<item>
  <title>Nera2</title>
  <link>https://www.deviantart.com/wlop/art/Nera2-1385671838</link>
  <media:thumbnail url="https://images.example/w_150/nera2-150.jpg" height="150" width="86"/>
  <media:thumbnail url="https://images.example/w_300/nera2-300w.jpg" height="522" width="300"/>
  <media:content url="https://images.example/w_677/nera2-pre.jpg?token=abc" height="1180" width="677" medium="image"/>
  <description>&lt;p&gt;O Nera&lt;/p&gt;&lt;div&gt;&lt;img src=&quot;https://images.example/w_115/nera2-200h.jpg&quot; /&gt;&lt;/div&gt;</description>
</item></channel></rss>`,
			want: []string{"https://images.example/w_677/nera2-pre.jpg?token=abc"},
		},
		{
			name: "media:group content",
			feed: `<?xml version="1.0"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel><title>t</title>
<item><title>g</title><media:group>
  <media:content url="https://img.example/a.png" type="image/png"/>
  <media:content url="https://img.example/clip.mp4" type="video/mp4"/>
  <media:content url="https://img.example/b.jpg"/>
  <media:thumbnail url="https://img.example/thumb.jpg"/>
</media:group></item></channel></rss>`,
			want: []string{"https://img.example/a.png", "https://img.example/b.jpg"},
		},
		{
			name: "artstation img in content:encoded",
			feed: `<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/"><channel><title>t</title>
<item><title>Nera2 by WLOP</title>
<description><![CDATA[<p></p>]]></description>
<content:encoded><![CDATA[<p><a href="https://www.artstation.com/artwork/RqK5Ye">View this on ArtStation</a></p><p><a href="https://cdna.artstation.com/p/large/wlop-5se.webp?1790588526"><img src="https://cdna.artstation.com/p/large/wlop-5se.webp?1790588526" alt=""/></a></p>]]></content:encoded>
<link>https://www.artstation.com/artwork/RqK5Ye</link>
</item></channel></rss>`,
			want: []string{"https://cdna.artstation.com/p/large/wlop-5se.webp?1790588526"},
		},
		{
			name: "tumblr multiple images, pixel and data URI dropped, relative resolved",
			feed: `<?xml version="1.0"?>
<rss version="2.0"><channel><title>t</title>
<item><title>post</title><link>https://staff.tumblr.com/post/1/slug</link>
<description>&lt;figure&gt;&lt;img src=&quot;https://64.media.tumblr.com/x/s640x960/one.png&quot; data-orig-height=&quot;800&quot;/&gt;&lt;/figure&gt;
&lt;img src=&quot;https://64.media.tumblr.com/x/s640x960/two.gif&quot;/&gt;
&lt;img src=&quot;https://64.media.tumblr.com/x/s640x960/one.png&quot;/&gt;
&lt;img src=&quot;https://track.example/p.gif&quot; width=&quot;1&quot; height=&quot;1&quot;/&gt;
&lt;img src=&quot;data:image/png;base64,AAAA&quot;/&gt;
&lt;img src=&quot;/local/three.jpg&quot; width=&quot;100%&quot;/&gt;</description>
</item></channel></rss>`,
			want: []string{
				"https://64.media.tumblr.com/x/s640x960/one.png",
				"https://64.media.tumblr.com/x/s640x960/two.gif",
				"https://staff.tumblr.com/local/three.jpg",
			},
		},
		{
			name: "pinterest img in description",
			feed: `<?xml version="1.0"?>
<rss version="2.0"><channel><title>t</title>
<item><title> </title><link>https://www.pinterest.com/pin/abc/</link>
<description>&lt;a href=&quot;https://www.pinterest.com/pin/abc/&quot;&gt;&lt;img src=&quot;https://i.pinimg.com/236x/65/3d/39/653d.jpg&quot;&gt;&lt;/a&gt; </description>
</item></channel></rss>`,
			want: []string{"https://i.pinimg.com/236x/65/3d/39/653d.jpg"},
		},
		{
			name: "image enclosure",
			feed: `<?xml version="1.0"?>
<rss version="2.0"><channel><title>t</title>
<item><title>e</title>
<enclosure url="https://img.example/song.mp3" type="audio/mpeg" length="1"/>
<enclosure url="https://img.example/cover.jpg" type="image/jpeg" length="1"/>
</item></channel></rss>`,
			want: []string{"https://img.example/cover.jpg"},
		},
		{
			name: "thumbnail only as last resort",
			feed: `<?xml version="1.0"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel><title>t</title>
<item><title>t</title><description>no pictures here</description>
<media:thumbnail url="https://img.example/thumb.jpg" width="150"/>
</item></channel></rss>`,
			want: []string{"https://img.example/thumb.jpg"},
		},
		{
			name: "only the largest of several thumbnails",
			feed: `<?xml version="1.0"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel><title>t</title>
<item><title>Karina Motorcycling</title>
<media:thumbnail url="https://img.example/w_300/karina-300w.jpg" height="169" width="300"/>
<media:thumbnail url="https://img.example/w_150/karina-150.jpg" height="84" width="150"/>
<media:thumbnail url="https://img.example/w_300/karina-200h.jpg" height="169" width="300"/>
<media:group><media:thumbnail url="https://img.example/w_600/karina-600.jpg" height="338" width="600"/></media:group>
</item></channel></rss>`,
			want: []string{"https://img.example/w_600/karina-600.jpg"},
		},
		{
			name: "thumbnails without sizes keep the first",
			feed: `<?xml version="1.0"?>
<rss version="2.0" xmlns:media="http://search.yahoo.com/mrss/"><channel><title>t</title>
<item><title>t</title>
<media:thumbnail url="https://img.example/a.jpg"/>
<media:thumbnail url="https://img.example/b.jpg"/>
</item></channel></rss>`,
			want: []string{"https://img.example/a.jpg"},
		},
		{
			name: "atom content",
			feed: `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom"><title>t</title>
<entry><title>a</title><id>1</id><link href="https://blog.example/posts/1"/>
<content type="html">&lt;img src=&quot;img/a.jpg&quot;&gt;</content>
</entry></feed>`,
			want: []string{"https://blog.example/posts/img/a.jpg"},
		},
		{
			name: "no images",
			feed: `<?xml version="1.0"?>
<rss version="2.0"><channel><title>t</title>
<item><title>plain</title><description>just &lt;b&gt;text&lt;/b&gt;</description></item>
</channel></rss>`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := parseItems(t, tt.feed)
			if len(items) != 1 {
				t.Fatalf("got %d items, want 1", len(items))
			}
			got := itemImages(items[0])
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}

			item := RSSItemFromGoFeedItem(items[0])
			wantFirst := ""
			if len(tt.want) > 0 {
				wantFirst = tt.want[0]
			}
			if item.Image != wantFirst {
				t.Errorf("Image = %q, want %q", item.Image, wantFirst)
			}
		})
	}
}

func TestItemImagesNil(t *testing.T) {
	if got := itemImages(nil); got != nil {
		t.Errorf("got %q, want nil", got)
	}
}
