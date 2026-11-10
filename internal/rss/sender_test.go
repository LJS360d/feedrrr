package rss

import (
	"encoding/json"
	"errors"
	htmltemplate "html/template"
	"reflect"
	"testing"
	"text/template"

	"github.com/nicholas-fedor/shoutrrr/pkg/services/chat/discord"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

func TestRenderTemplateEngines(t *testing.T) {
	const tmpl = `{{.Title}} {{.Link}}`
	item := RSSItem{Title: `Rock'n'roll & <b>`, Link: "https://example.com/?a=1&b=2"}

	tests := []struct {
		name string
		tmpl Template
		want string
	}{
		{
			name: "html",
			tmpl: htmltemplate.Must(htmltemplate.New("t").Parse(tmpl)),
			want: `Rock&#39;n&#39;roll &amp; &lt;b&gt; https://example.com/?a=1&amp;b=2`,
		},
		{
			name: "text",
			tmpl: template.Must(template.New("t").Parse(tmpl)),
			want: `Rock'n'roll & <b> https://example.com/?a=1&b=2`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &BaseMessageSender{tmpl: tt.tmpl}
			got, err := s.render(item)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

type fakeRouter struct {
	sent   []string
	items  [][]types.MessageItem
	titles []string
}

func (r *fakeRouter) Send(msg string, params *types.Params) []error {
	r.sent = append(r.sent, msg)
	r.titles = append(r.titles, (*params)["title"])
	return nil
}

func (r *fakeRouter) SendItems(items []types.MessageItem, params types.Params) []error {
	r.items = append(r.items, items)
	r.titles = append(r.titles, params["title"])
	return nil
}

func embedTemplate(t *testing.T, embeds *EmbedCollector, text string) Template {
	t.Helper()
	funcs := map[string]any{"embed": embeds.Embed}
	return template.Must(template.New("t").Funcs(funcs).Parse(text))
}

func TestInstantSenderEmbeds(t *testing.T) {
	embeds := NewEmbedCollector(false)
	tmpl := embedTemplate(t, embeds, `{{.Title}} {{.Link}}{{ range .Images }}{{ embed . }}{{ end }}`)
	r := &fakeRouter{}
	s := NewInstantSender(r, tmpl, embeds, nil)

	images := []string{"https://i/1", "https://i/2", "https://i/3", "https://i/4", "https://i/5"}
	must(t, s.Enqueue("with images", RSSItem{Title: "a", Link: "https://l/a", Images: images, Image: images[0]}))
	must(t, s.Enqueue("no images", RSSItem{Title: "b", Link: "https://l/b"}))
	flush(t, s)

	// the queue is sent in reverse (feeds list newest first), so b goes out first
	if len(r.sent) != 1 || r.sent[0] != "b https://l/b" {
		t.Errorf("plain sends = %q", r.sent)
	}
	if len(r.items) != 1 {
		t.Fatalf("rich sends = %d, want 1", len(r.items))
	}
	items := r.items[0]
	if len(items) != MaxEmbeds {
		t.Fatalf("got %d embed items, want %d", len(items), MaxEmbeds)
	}
	if items[0].Text != "a https://l/a" {
		t.Errorf("first item text = %q", items[0].Text)
	}
	for i, item := range items {
		if i > 0 && item.Text != "" {
			t.Errorf("item %d has text %q", i, item.Text)
		}
		if len(item.Fields) != 1 || item.Fields[0].Key != "embed_image_url" || item.Fields[0].Value != images[i] {
			t.Errorf("item %d fields = %+v", i, item.Fields)
		}
	}
	if !reflect.DeepEqual(r.titles, []string{"no images", "with images"}) {
		t.Errorf("titles = %q", r.titles)
	}

	// shoutrrr turns them into one Discord message: text and title on the
	// first embed, one image per embed
	payload, err := discord.CreatePayloadFromItems(items, "with images", [types.MessageLevelCount]uint{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	var got struct {
		Content string `json:"content"`
		Embeds  []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Image       struct {
				URL string `json:"url"`
			} `json:"image"`
		} `json:"embeds"`
	}
	must(t, json.Unmarshal(raw, &got))
	if got.Content != "" || len(got.Embeds) != MaxEmbeds {
		t.Fatalf("payload = %s", raw)
	}
	if got.Embeds[0].Title != "with images" || got.Embeds[0].Description != "a https://l/a" {
		t.Errorf("first embed = %+v", got.Embeds[0])
	}
	for i, e := range got.Embeds {
		if e.Image.URL != images[i] {
			t.Errorf("embed %d image = %q", i, e.Image.URL)
		}
	}
}

func TestEmbedIgnoresEmpty(t *testing.T) {
	embeds := NewEmbedCollector(false)
	tmpl := embedTemplate(t, embeds, `{{.Title}}{{ embed .Image }}`)
	r := &fakeRouter{}
	s := NewInstantSender(r, tmpl, embeds, nil)

	must(t, s.Enqueue("t", RSSItem{Title: "x"}))
	flush(t, s)
	if len(r.items) != 0 || !reflect.DeepEqual(r.sent, []string{"x"}) {
		t.Errorf("sent = %q, items = %v", r.sent, r.items)
	}
}

func TestEmbedHTMLEngine(t *testing.T) {
	embeds := NewEmbedCollector(false)
	funcs := map[string]any{"embed": embeds.Embed}
	tmpl := htmltemplate.Must(htmltemplate.New("t").Funcs(funcs).Parse(`{{.Title}}{{ embed .Image }}`))
	r := &fakeRouter{}
	s := NewInstantSender(r, tmpl, embeds, nil)

	must(t, s.Enqueue("t", RSSItem{Title: "x", Image: "https://i/1?a=1&b=2"}))
	flush(t, s)
	if len(r.items) != 1 || r.items[0][0].Fields[0].Value != "https://i/1?a=1&b=2" {
		t.Errorf("items = %+v", r.items)
	}
}

func TestBatchedSenderEmbedsInline(t *testing.T) {
	embeds := NewEmbedCollector(true)
	tmpl := embedTemplate(t, embeds, `{{.Title}}
{{ range .Images }}{{ embed . }}{{ end }}`)
	r := &fakeRouter{}
	s := NewBatchedSender(r, tmpl, embeds)

	must(t, s.Enqueue("a", RSSItem{Title: "a", Images: []string{"https://i/1", "https://i/2"}}))
	must(t, s.Enqueue("b", RSSItem{Title: "b", Images: []string{"https://i/3"}}))
	flush(t, s)

	want := []string{"b\nhttps://i/3\n\na\nhttps://i/1\nhttps://i/2\n"}
	if !reflect.DeepEqual(r.sent, want) {
		t.Errorf("sent = %q, want %q", r.sent, want)
	}
	if len(r.items) != 0 {
		t.Errorf("batched sender sent rich items: %v", r.items)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func flush(t *testing.T, s MessageSender) {
	t.Helper()
	failed, err := s.Flush()
	if err != nil || len(failed) > 0 {
		t.Fatalf("Flush() = %v, %v", failed, err)
	}
}

// failingRouter fails every send whose title is in fail
type failingRouter struct {
	fakeRouter
	fail map[string]bool
}

func (r *failingRouter) Send(msg string, params *types.Params) []error {
	if r.fail[(*params)["title"]] {
		return []error{errors.New("rate limited")}
	}
	return r.fakeRouter.Send(msg, params)
}

func TestInstantSenderReportsFailedItems(t *testing.T) {
	r := &failingRouter{fail: map[string]bool{"b": true}}
	s := NewInstantSender(r, template.Must(template.New("t").Parse(`{{.Title}}`)), nil, nil)

	for _, title := range []string{"a", "b", "c"} {
		must(t, s.Enqueue(title, RSSItem{Title: title, GUID: "guid-" + title}))
	}
	failed, err := s.Flush()
	if err == nil {
		t.Fatal("want error")
	}
	if len(failed) != 1 || failed[0].GUID != "guid-b" {
		t.Errorf("failed = %+v", failed)
	}
	if !reflect.DeepEqual(r.sent, []string{"c", "a"}) {
		t.Errorf("sent = %q", r.sent)
	}
}

func TestBatchedSenderReportsFailure(t *testing.T) {
	r := &failingRouter{fail: map[string]bool{"": true}}
	s := NewBatchedSender(r, template.Must(template.New("t").Parse(`{{.Title}}`)), nil)

	must(t, s.Enqueue("a", RSSItem{Title: "a", GUID: "ga"}))
	must(t, s.Enqueue("b", RSSItem{Title: "b", GUID: "gb"}))
	failed, err := s.Flush()
	if err == nil || len(failed) != 2 {
		t.Fatalf("Flush() = %+v, %v; want both items failed", failed, err)
	}

	// an empty queue sends nothing
	failed, err = s.Flush()
	if err != nil || failed != nil || len(r.sent) != 0 {
		t.Fatalf("empty Flush() = %v, %v, sent %q", failed, err, r.sent)
	}
}
