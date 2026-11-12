package config

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type (
	ChangeDetectionMode int
	SenderMode          int
	TemplateEngine      int
)

const (
	ChangeModeGUID ChangeDetectionMode = iota
	ChangeModePubDate
)

const (
	SenderModeInstant SenderMode = iota
	SenderModeBatched
)

const (
	// TemplateEngineHTML escapes output for HTML contexts (safe for sinks that render HTML)
	TemplateEngineHTML TemplateEngine = iota
	// TemplateEngineText renders verbatim (for plain-text/markdown sinks like Discord)
	TemplateEngineText
)

const (
	DefaultTemplateEngine             = TemplateEngineHTML
	DefaultChangeDetectionMode        = ChangeModeGUID
	DefaultSenderMode                 = SenderModeInstant
	DefaultFetchTimeout               = 60 * time.Second
	DefaultMessageTemplate     string = `{{.Title}} ({{.PubDate}})

{{ html2text (or .Content .Description "no content") }}

{{.Link}}
`
)

var changeModeMap = map[string]ChangeDetectionMode{
	"pubdate": ChangeModePubDate,
	"guid":    ChangeModeGUID,
}

var senderModeMap = map[string]SenderMode{
	"instant": SenderModeInstant,
	"batched": SenderModeBatched,
}

var templateEngineMap = map[string]TemplateEngine{
	"html": TemplateEngineHTML,
	"text": TemplateEngineText,
}

type FeedrrrConfig struct {
	Jobs    map[string]JobConfig `mapstructure:"jobs"`
	Sinks   map[string][]string  `mapstructure:"sinks"`
	SendGap time.Duration        `mapstructure:"send_gap"`
	// MaxConcurrentJobs caps jobs running at once; 0 means no limit
	MaxConcurrentJobs uint `mapstructure:"max_concurrent_jobs"`
	// StateDir keeps job state across restarts; empty keeps it in memory only
	StateDir string `mapstructure:"state_dir"`
}

type JobConfig struct {
	Sinks              []string            `mapstructure:"sinks"`
	Schedule           string              `mapstructure:"schedule"`
	Source             string              `mapstructure:"source"`
	Prefix             string              `mapstructure:"prefix,omitempty"` // title prefix
	ChangeMode         ChangeDetectionMode `mapstructure:"change_mode"`
	SenderMode         SenderMode          `mapstructure:"sender_mode"`
	MessageTemplate    string              `mapstructure:"msg_template"`
	TemplateEngine     TemplateEngine      `mapstructure:"template_engine"`
	SendFutureArticles bool                `mapstructure:"future_articles"`
	FetchTimeout       time.Duration       `mapstructure:"fetch_timeout"`
	RequireImage       bool                `mapstructure:"require_image"` // skip items without images
	HideTitle          bool                `mapstructure:"hide_title"`    // send items without a title
	Backfill           int                 `mapstructure:"backfill"`      // send the newest N items on the first run
	UploadImages       bool                `mapstructure:"upload_images"` // attach embed images as files
	ImageReferer       string              `mapstructure:"image_referer"` // Referer sent when downloading them
	ImageRewrites      []ImageRewrite      `mapstructure:"image_rewrites"`
}

// ImageRewrite rewrites image URLs matching a regular expression
type ImageRewrite struct {
	Match   string `mapstructure:"match"`   // Go regexp
	Replace string `mapstructure:"replace"` // $1, ${name} refer to groups
	// Fallback rules only apply when an image can't be attached (upload_images)
	Fallback bool `mapstructure:"fallback"`
}

func (t *ChangeDetectionMode) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}

	if str == "" {
		*t = DefaultChangeDetectionMode
		return nil
	}

	mappedVal, ok := changeModeMap[strings.TrimSpace(str)]

	if !ok {
		return fmt.Errorf("Invalid value for change detection mode: %s", str)
	}

	*t = mappedVal
	return nil
}

func (t *SenderMode) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}

	if str == "" {
		*t = DefaultSenderMode
		return nil
	}

	mappedVal, ok := senderModeMap[strings.TrimSpace(str)]

	if !ok {
		return fmt.Errorf("Invalid value for sender mode: %s", str)
	}

	*t = mappedVal
	return nil
}

func (t *TemplateEngine) UnmarshalMapstructure(input any) error {
	str, ok := input.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", input)
	}

	if str == "" {
		*t = DefaultTemplateEngine
		return nil
	}

	mappedVal, ok := templateEngineMap[strings.TrimSpace(str)]

	if !ok {
		return fmt.Errorf("Invalid value for template engine: %s", str)
	}

	*t = mappedVal
	return nil
}

func ParseConfig(appName string, configFileOverride string) (*FeedrrrConfig, error) {
	appName = strings.ToValidUTF8(strings.ToLower(appName), "")

	if configFileOverride != "" {
		viper.SetConfigFile(configFileOverride)
		slog.Info("Using explicit config file path", "configFileOverride", configFileOverride)
	} else {
		searchPaths := []string{
			fmt.Sprintf("/etc/%v/", appName),

			// respect XDG base directory specification https://specifications.freedesktop.org/basedir/latest/#variables
			fmt.Sprintf("$XDG_CONFIG_HOME/%v/", appName),
			fmt.Sprintf("$HOME/.config/%v/", appName),
			".",
		}

		setupDefaultConfigPaths(appName, searchPaths)
		slog.Info("Using default config search paths", "searchPaths", searchPaths)
	}

	err := viper.ReadInConfig()
	if err != nil {
		return nil, err
	}

	c := &FeedrrrConfig{}
	err = viper.UnmarshalExact(c)
	if err != nil {
		return nil, err
	}

	return c, nil
}

func setupDefaultConfigPaths(appName string, searchPaths []string) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")

	for _, searchPath := range searchPaths {
		viper.AddConfigPath(searchPath)
	}

	viper.SetEnvPrefix(appName)
	viper.AutomaticEnv()
}
