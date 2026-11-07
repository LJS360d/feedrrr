package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfigTemplateEngine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	yml := `jobs:
  plain:
    source: https://example.com/a.xml
    schedule: "* * * * *"
    sinks: [s]
  text:
    source: https://example.com/b.xml
    schedule: "* * * * *"
    sinks: [s]
    template_engine: text
  html:
    source: https://example.com/c.xml
    schedule: "* * * * *"
    sinks: [s]
    template_engine: html
sinks:
  s: logger://
`
	if err := os.WriteFile(path, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}

	c, err := ParseConfig("feedrrr", path)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]TemplateEngine{
		"plain": DefaultTemplateEngine,
		"text":  TemplateEngineText,
		"html":  TemplateEngineHTML,
	}
	for job, engine := range want {
		if got := c.Jobs[job].TemplateEngine; got != engine {
			t.Errorf("job %q: template engine = %v, want %v", job, got, engine)
		}
	}
}
