package rss

import (
	htmltemplate "html/template"
	"testing"
	"text/template"
)

func TestRenderWithTemplateEngines(t *testing.T) {
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
			got, err := s.RenderWithTemplate(item)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
