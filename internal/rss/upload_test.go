package rss

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInstantSenderUploadsImages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://ref/" {
			http.Error(w, "no referer", http.StatusForbidden)
			return
		}
		switch r.URL.Path {
		case "/small.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("png-data"))
		case "/img-original/img/2026/x_p0.png": // over the limit
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte(strings.Repeat("x", 200)))
		case "/img-master/img/2026/x_p0_master1200.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpg-data"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	// fallback to a smaller size
	rules := []ImageRewrite{{
		Match:    regexp.MustCompile(`^(https?://[^/]+)/img-original/(img/.+_p\d+)\.\w+$`),
		Replace:  "$1/img-master/${2}_master1200.jpg",
		Fallback: true,
	}}
	uploader := NewImageUploader("https://ref/", 5*time.Second, rules)
	uploader.limit = 300 // 100 per image

	embeds := NewEmbedCollector(false)
	tmpl := embedTemplate(t, embeds, `{{.Link}}{{ range .Images }}{{ embed . }}{{ end }}`)
	r := &fakeRouter{}
	s := NewInstantSender(r, tmpl, embeds, uploader)

	images := []string{srv.URL + "/small.png", srv.URL + "/img-original/img/2026/x_p0.png", srv.URL + "/missing.png"}
	must(t, s.Enqueue("", RSSItem{Link: "https://l/a", Images: images}))
	flush(t, s)

	if len(r.items) != 1 || len(r.items[0]) != 2 {
		t.Fatalf("items = %v", r.items)
	}
	items := r.items[0]
	want := []struct{ url, name, data string }{
		{"attachment://image0.png", "image0.png", "png-data"},
		{"attachment://image1.jpg", "image1.jpg", "jpg-data"}, // fallback rule
		// missing.png is left out
	}
	for i, w := range want {
		var got string
		for _, f := range items[i].Fields {
			if f.Key == "embed_image_url" {
				got = f.Value
			}
		}
		if got != w.url {
			t.Errorf("item %d embed url = %q, want %q", i, got, w.url)
		}
		if f := items[i].File; f == nil || f.Name != w.name || string(f.Data) != w.data {
			t.Errorf("item %d file = %+v, want %s %q", i, f, w.name, w.data)
		}
	}
}

func TestUploadShareCarriesOver(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size := map[string]int{"/a": 10, "/b": 150}[r.URL.Path]
		_, _ = w.Write([]byte(strings.Repeat("x", size)))
	}))
	t.Cleanup(srv.Close)
	u := NewImageUploader("", 5*time.Second, nil)
	u.limit = 200 // 100 each; b only fits with what a left unused

	urls, files := u.prepare(t.Context(), []string{srv.URL + "/a", srv.URL + "/b"})
	if len(urls) != 2 || len(files[1].Data) != 150 {
		t.Fatalf("urls %v", urls)
	}
}

func TestUploadNothingAttachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	embeds := NewEmbedCollector(false)
	r := &fakeRouter{}
	s := NewInstantSender(r, embedTemplate(t, embeds, `{{.Link}}{{ range .Images }}{{ embed . }}{{ end }}`), embeds, NewImageUploader("", 5*time.Second, nil))
	must(t, s.Enqueue("", RSSItem{Link: "https://l/a", Images: []string{srv.URL + "/x.png"}}))
	flush(t, s)
	if len(r.items) != 1 || len(r.items[0]) != 1 || r.items[0][0].Text != "https://l/a" || len(r.items[0][0].Fields) != 0 {
		t.Fatalf("items = %+v, want just the text", r.items)
	}
}

func TestImageExt(t *testing.T) {
	for _, tc := range []struct{ ct, url, want string }{
		{"image/jpeg", "https://x/a.png", ".jpg"},
		{"image/webp; charset=binary", "https://x/a", ".webp"},
		{"application/octet-stream", "https://x/a.PNG?v=1", ".png"},
		{"", "https://x/a", ".jpg"},
	} {
		if got := imageExt(tc.ct, tc.url); got != tc.want {
			t.Errorf("imageExt(%q, %q) = %q, want %q", tc.ct, tc.url, got, tc.want)
		}
	}
}

func TestImageRewrites(t *testing.T) {
	rules := []ImageRewrite{
		{Match: regexp.MustCompile(`/large/`), Replace: "/medium/"},
		{Match: regexp.MustCompile(`\.png$`), Replace: ".jpg"},
		{Match: regexp.MustCompile(`/medium/`), Replace: "/small/", Fallback: true},
	}
	got := rewriteImages(rules, []string{"https://x/large/a.png", "https://x/other/b.gif"})
	if want := []string{"https://x/medium/a.jpg", "https://x/other/b.gif"}; !slices.Equal(got, want) {
		t.Errorf("rewriteImages = %v, want %v", got, want)
	}
	if got := fallbackImages(rules, "https://x/medium/a.jpg"); !slices.Equal(got, []string{"https://x/small/a.jpg"}) {
		t.Errorf("fallbackImages = %v", got)
	}
	if got := fallbackImages(rules, "https://x/other/b.gif"); got != nil {
		t.Errorf("fallbackImages without match = %v", got)
	}
}
