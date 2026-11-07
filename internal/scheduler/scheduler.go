package scheduler

import (
	"context"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"net/url"
	"strings"
	"text/template"

	"dev.kaesebrot.eu/go/feedrrr/internal/config"
	"dev.kaesebrot.eu/go/feedrrr/internal/rss"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
)

func SetupJobs(ctx context.Context, jobConfigs *map[string]config.JobConfig, jobSinks *map[string]*router.ServiceRouter) (gocron.Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, err
	}

	for name, cfg := range *jobConfigs {
		router, exists := (*jobSinks)[name]

		if !exists {
			return nil, fmt.Errorf("Couldn't get associated job router!")
		}

		url, err := url.Parse(cfg.Source)
		if err != nil {
			return nil, err
		}

		prefix := cfg.Prefix
		if prefix != "" {
			prefix += " "
		}

		templateStr := cfg.MessageTemplate
		if templateStr == "" {
			templateStr = config.DefaultMessageTemplate
		}

		funcs := map[string]any{"html2text": html2text.HTML2Text}
		var tmpl rss.Template
		switch cfg.TemplateEngine {
		case config.TemplateEngineText:
			tmpl, err = template.New("rss_message").Funcs(funcs).Parse(templateStr)
		default:
			tmpl, err = htmltemplate.New("rss_message").Funcs(funcs).Parse(templateStr)
		}
		if err != nil {
			return nil, err
		}

		logger := slog.Default().With("job", name)

		var sender rss.MessageSender
		switch cfg.SenderMode {
		case config.SenderModeInstant:
			sender = rss.NewInstantSender(router, tmpl)
		case config.SenderModeBatched:
			sender = rss.NewBatchedSender(router, tmpl)
		}

		fetchTimeout := cfg.FetchTimeout
		if fetchTimeout <= 0 {
			// 60s
			fetchTimeout = config.DefaultFetchTimeout
		}

		var rssJob rss.FeedPoller
		switch cfg.ChangeMode {
		case config.ChangeModeGUID:
			rssJob = rss.NewGUIDJob(*logger, *url, prefix, fetchTimeout, sender)
		case config.ChangeModePubDate:
			rssJob = rss.NewPubDateJob(*logger, *url, prefix, cfg.SendFutureArticles, fetchTimeout, sender)
		}

		// A feed that fails at startup (timeout, 4xx/5xx, bad XML, DNS...) must not
		// take the whole daemon down: the job seeds its state on the first
		// successful scheduled fetch instead
		if err := rssJob.Init(ctx); err != nil {
			logger.Warn("Initial fetch failed, will retry on schedule", "err", err)
		}

		// format: * * * * *   -> without seconds (5 elements)
		//         * * * * * * -> with seconds (6 elements)
		scheduleSplit := strings.Split(cfg.Schedule, " ")
		if strings.HasPrefix(scheduleSplit[0], "TZ=") || strings.HasPrefix(scheduleSplit[0], "CRON_TZ=") {
			scheduleSplit = scheduleSplit[1:]
		}
		withSeconds := len(scheduleSplit) > 5

		j, err := s.NewJob(
			gocron.CronJob(cfg.Schedule, withSeconds),
			gocron.NewTask(func(contxt context.Context) error {
				return rssJob.RetrieveAndSendNewItems(contxt)
			}),
			gocron.WithName(name),
			gocron.WithContext(ctx),
			gocron.WithEventListeners(
				gocron.BeforeJobRuns(func(jobID uuid.UUID, jobName string) {
					slog.Debug("Running job", "jobID", jobID, "jobName", jobName)
				}),
				gocron.AfterJobRuns(func(jobID uuid.UUID, jobName string) {
					slog.Debug("Job ran successfully", "jobID", jobID, "jobName", jobName)
				}),
				gocron.AfterJobRunsWithError(
					func(jobID uuid.UUID, jobName string, err error) {
						slog.Error("Job returned an error", "jobID", jobID, "jobName", jobName, "err", err)
					},
				),
			),
		)
		if err != nil {
			return nil, err
		}

		slog.Info("Added cronjob to scheduler", "name", name, "id", j.ID(), "schedule", cfg.Schedule)
	}

	return s, nil
}
