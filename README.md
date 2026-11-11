# feedrrr

feedrrr is an RSS feed polling daemon that periodically checks configured RSS feeds on a cron schedule and sends new items to configurable notification sinks using [Shoutrrr](https://github.com/nicholas-fedor/shoutrrr).

## Features
- crontab-based syntax for jobs
- multiple notification sinks and aliases using [Shoutrrr](https://github.com/nicholas-fedor/shoutrrr). See its [documentation page](https://shoutrrr.nickfedor.com/latest/services/overview/) for supported services.
- plaintext or source (usually HTML) formatting in delivery
- image extraction from feed items, sent as image embeds (see [Image feeds](#image-feeds))

## Installation

### Docker
You may either use a docker image:

```bash
ghcr.io/das-kaesebrot/feedrrr
```

Example command to run it:

```bash
docker run --rm -it -v /path/to/config.yml:/etc/feedrrr/config.yml ghcr.io/das-kaesebrot/feedrrr
```

### Go install

Or go's install syntax:
```bash
go install dev.kaesebrot.eu/go/feedrrr/cmd/feedrrr@latest
```

## Build and install

### Build from source

Clone the repository and build the binary:

```bash
git clone https://github.com/das-kaesebrot/feedrrr.git
cd feedrrr
go build -o feedrrr ./cmd/feedrrr
./feedrrr -c config.yml
```

## Configuration

feedrrr is configured via a YAML configuration file. By default, it looks for `config.yml` in the following locations (in order of precedence):

1. `/etc/feedrrr/config.yml`
2. `$XDG_CONFIG_HOME/feedrrr/config.yml`
3. `$HOME/.config/feedrrr/config.yml`
4. `./config.yml` (current directory)

See [`config.example.yml`](config.example.yml) for a full example.

### Configuration

### Environment variables

| Variable | Description | Default |
|---|---|---|
| `LOG_LEVEL` | Log level (`debug`, `info`, `warn`, `error`) | `info` |

All configuration keys can be overridden via environment variables prefixed with `FEEDRRR_` (e.g. `FEEDRRR_JOBS`).

### CLI flags

| Flag | Short | Description |
|---|---|---|
| `--config` | `-c` | Explicitly override the configuration file used |
| `--loglevel` | `-l` | Log level |

#### YAML config

```yaml
# Minimum time between two messages, across all jobs (optional, default: 1s)
# sends are serialized; the gap is counted from the end of the previous send.
send_gap: 500ms
```

##### Jobs

Each job defines an RSS feed to poll and where to send notifications.

```yaml
jobs:
  my-feed:
    # RSS/Atom feed URL (required)
    source: https://example.com/feed.xml

    # Cron expression using either 5 or 6-elements (with seconds) syntax (required)
    schedule: "*/30 * * * *"

    # List of sink aliases or Shoutrrr URLs (required)
    sinks:
      - my-telegram

    # Title prefix (optional)
    prefix: "[New Post]"

    # Change detection mode (optional, default: guid)
    # can be either "guid" or "pubdate"
    # pubdate: articles published (specifically, the pubdate value) between last cronjob run and current cron job run will be detected as new
    # guid: articles whose guid (or link, or title) hasn't been in the feed during the last 7 days will be detected as new
    change_mode: guid

    # Message sender mode (optional, default: instant)
    # can be either "instant" or "batched"
    # instant: each new item is sent as an individual message
    # batched: all new items are sent in one combined message
    sender_mode: instant

    # HTTP timeout for fetching the feed (optional, default: 60s)
    # a failed initial fetch (timeout, HTTP error, bad XML...) is retried on schedule
    fetch_timeout: 60s

    # Skip items that have no image (optional, default: false)
    require_image: false

    # Send the newest N items of the feed on the first run (optional, default: 0)
    # applies on every restart, since seen state isn't persisted
    backfill: 0

    # Attach images passed to `embed` as files instead of linking them
    # (optional, default: false, instant sender mode only)
    # for image hosts the sink can't load itself. Up to 10 MB per message, shared
    # between images; an image over its share is retried with the fallback
    # image_rewrites, and left out if it still doesn't fit
    upload_images: false

    # Referer header sent when downloading images for upload_images (optional)
    image_referer: ""

    # Rewrite image URLs (optional), applied in order, e.g. to pick a smaller size
    # match is a Go regexp, replace can refer to its groups ($1, ${name})
    # fallback: true rules only apply with upload_images, when an image can't be attached
    image_rewrites:
      - match: '^(https://img\.example\.com)/original/(.+)\.\w+$'
        replace: '$1/large/$2.jpg'
        fallback: false

    # Send only the rendered msg_template, without a title (optional, default: false)
    hide_title: false

    # Template engine for msg_template (optional, default: html)
    # can be either "html" or "text"
    # html: html/template, values are HTML-escaped; use for sinks that render HTML
    #       (e.g. telegram with parsemode=HTML, smtp with usehtml=yes)
    # text: text/template, values are rendered verbatim; use for plain-text/markdown
    #       sinks (e.g. discord, ntfy, gotify), where HTML escaping shows up as &amp; etc.
    #       the builtin {{html .Title}} function can still escape individual values
    template_engine: html

    # Item message template (optional, default: see /internal/confg/config.go:DefaultMessageTemplate)
    # can be a multiline YAML string
    # Available properties:
    #
    # 	Title       string
    # 	PubDate     time.Time
    # 	Content     string
    # 	Description string
    # 	GUID        string
    # 	Link        string
    # 	Images      []string // image URLs found in the item, see "Image feeds"
    # 	Image       string   // first entry of Images, or empty
    #
    # Available functions:
    #   html2text(html string) string // generates plaintext from html
    #   embed(url string) string      // attaches an image as an embed, see "Image feeds"
    #
    msg_template: |
      Item with a custom feed message appeared:
      {{.Title}}

      {{ html2text (or .Content .Description "no content") }}

      {{.Link}}
```

The `schedule` field accepts standard 5-field cron expressions (`minute hour dom month dow`) as well as 6-field expressions with seconds (`second minute hour dom month dow`). Timezone-aware schedules are supported via a `TZ=` or `CRON_TZ=` prefix (e.g. `TZ=Europe/Berlin 0 9 * * *`).

For the full reference on the templating engine, please see [html/template](https://pkg.go.dev/html/template) (`template_engine: html`, the default) or [text/template](https://pkg.go.dev/text/template) (`template_engine: text`).
The default message template is:
```go
{{.Title}} ({{.PubDate}})

{{ html2text (or .Content .Description "no content") }}

{{.Link}}
```

The context for the template is `rss.RSSItem` in [item.go](./internal/rss/item.go):
```go
type RSSItem struct {
	Title       string
	PubDate     time.Time
	Content     string
	Description string
	GUID        string
	Link        string
	Images      []string
	Image       string
}
```

##### Image feeds

Every item's image URLs are extracted into `.Images` (deduplicated, best first) and `.Image` (the first one, or empty), from the first of these that yields any:

1. `<media:content>` (also inside `<media:group>`) and image `<enclosure>`s
2. `<img>` tags in the content, then the description
3. `<media:thumbnail>`

Tracking pixels (declared 1px wide or high) and non-http(s) URLs are dropped, relative URLs are resolved against the item link.

Call `embed` in the template to send an image as an embed instead of text (at most 4 per item, empty URLs ignored). Sinks with embed support (e.g. Discord) get the title and text on the first embed, one image each; others get only the text. With `sender_mode: batched`, `embed` writes the URL inline instead.

Example, skipping posts without images:

```yaml
jobs:
  gallery:
    source: https://example.com/gallery.rss
    schedule: "*/30 * * * *"
    sinks:
      - discord-art
    template_engine: text
    require_image: true
    msg_template: |
      {{.Link}}
      {{ range .Images }}{{ embed . }}{{ end }}

sinks:
  discord-art: discord://token@webhookid
```

##### Sinks

Sinks map aliases to one or more Shoutrrr URLs [(reference)](https://shoutrrr.nickfedor.com/latest/services/overview/) and/or one or more aliases.

```yaml
sinks:
  my-telegram: telegram://bottoken?chats=chatid
  my-email: smtp://user:password@smtp.example.com:587/?from=alerts@example.com&to=me@example.com
  both-combined:
    - my-telegram
    - my-email
```
