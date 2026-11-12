package scheduler

import (
	"strings"
	"testing"
	"text/template"

	"dev.kaesebrot.eu/go/feedrrr/internal/rss"
)

func TestTemplateReplace(t *testing.T) {
	tmpl := template.Must(template.New("t").Funcs(templateFuncs(rss.NewEmbedCollector(false))).
		Parse(`{{ replace (replace .Link "://twitter.com/" "://fixupx.com/") "://x.com/" "://fixupx.com/" }}`))

	cases := map[string]string{
		"https://x.com/user/status/1":       "https://fixupx.com/user/status/1",
		"https://twitter.com/user/status/1": "https://fixupx.com/user/status/1",
		"https://example.com/x.com/a":       "https://example.com/x.com/a",
	}
	for in, want := range cases {
		var b strings.Builder
		if err := tmpl.Execute(&b, struct{ Link string }{in}); err != nil {
			t.Fatal(err)
		}
		if got := b.String(); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
}
