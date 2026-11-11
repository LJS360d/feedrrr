package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestParseConfigRequireImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	yml := `jobs:
  art:
    source: https://example.com/a.xml
    schedule: "* * * * *"
    sinks: [s]
    require_image: true
    hide_title: true
  news:
    source: https://example.com/b.xml
    schedule: "* * * * *"
    sinks: [s]
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
	if !c.Jobs["art"].RequireImage {
		t.Error("art: require_image not set")
	}
	if !c.Jobs["art"].HideTitle {
		t.Error("art: hide_title not set")
	}
	if c.Jobs["news"].RequireImage || c.Jobs["news"].HideTitle {
		t.Error("news: require_image should default to false")
	}
}

func TestParseConfigSendGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	yml := `send_gap: 2s
jobs:
  a:
    source: https://example.com/a.xml
    schedule: "* * * * *"
    sinks: [s]
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
	if c.SendGap != 2*time.Second {
		t.Errorf("send_gap = %v, want 2s", c.SendGap)
	}
}
