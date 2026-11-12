package scheduler

import (
	"strconv"
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

func TestHashSchedule(t *testing.T) {
	got := hashSchedule("job", "TZ=UTC H H/15 * * * *")
	if got != hashSchedule("job", "TZ=UTC H H/15 * * * *") {
		t.Error("hashed schedule should be stable")
	}
	f := strings.Fields(got)
	if len(f) != 7 || f[0] != "TZ=UTC" || strings.Contains(got, "H") || !strings.HasSuffix(f[2], "/15") {
		t.Fatalf("got %q", got)
	}
	if s := hashSchedule("job", "*/5 * * * *"); s != "*/5 * * * *" {
		t.Errorf("schedule without H changed to %q", s)
	}
	seen := map[string]bool{}
	for i := range 50 {
		seen[hashSchedule(strconv.Itoa(i), "H * * * *")] = true
	}
	if len(seen) < 20 {
		t.Errorf("only %d distinct minutes for 50 jobs", len(seen))
	}
}

func TestHashScheduleFieldsIndependent(t *testing.T) {
	// a weak hash made the second track the minute, so jobs clumped
	slots := map[string]bool{}
	near := 0
	for i := range 200 {
		f := strings.Fields(hashSchedule("job_"+strconv.Itoa(i), "H H * * * *"))
		slots[f[0]+" "+f[1]] = true
		sec, _ := strconv.Atoi(f[0])
		min, _ := strconv.Atoi(f[1])
		if d := sec - min; d >= -1 && d <= 1 {
			near++
		}
	}
	// 200 jobs over 3600 slots: ~195 distinct expected
	if len(slots) < 180 {
		t.Errorf("only %d distinct second+minute slots for 200 jobs", len(slots))
	}
	// ~3/60 of jobs expected by chance
	if near > 30 {
		t.Errorf("%d of 200 jobs have second within 1 of minute", near)
	}
}
