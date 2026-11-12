package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	htmltemplate "html/template"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"dev.kaesebrot.eu/go/feedrrr/internal/config"
	"dev.kaesebrot.eu/go/feedrrr/internal/rss"
	"github.com/go-co-op/gocron/v2"
	"github.com/google/uuid"
	"github.com/k3a/html2text"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
)

func SetupJobs(ctx context.Context, c *config.FeedrrrConfig, jobSinks *map[string]*router.ServiceRouter, jobURLs map[string][]string) (gocron.Scheduler, error) {
	// running jobs get their context cancelled on shutdown and then have this
	// long to finish sending and save their state
	opts := []gocron.SchedulerOption{gocron.WithStopTimeout(StopTimeout)}
	if c.MaxConcurrentJobs > 0 {
		opts = append(opts, gocron.WithLimitConcurrentJobs(c.MaxConcurrentJobs, gocron.LimitModeWait))
		if c.MaxFetchesPerHost > 0 {
			slog.Warn("max_concurrent_jobs with max_fetches_per_host: jobs waiting for a host keep their slot, " +
				"so a slow host can delay every other job; prefer max_fetches_per_host alone")
		}
	}
	s, err := gocron.NewScheduler(opts...)
	if err != nil {
		return nil, err
	}
	if err := prepareStateDir(c.StateDir); err != nil {
		return nil, err
	}
	if c.StateDir == "" {
		slog.Info("No state_dir set: job state is kept in memory only, restarts reseed every feed")
	} else {
		slog.Info("Persisting job state", "state_dir", c.StateDir)
	}

	var inits []func() bool
	restored := 0
	for name, cfg := range c.Jobs {
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

		var rewrites []rss.ImageRewrite
		for i, r := range cfg.ImageRewrites {
			re, err := regexp.Compile(r.Match)
			if err != nil {
				return nil, fmt.Errorf("job %s: image_rewrites[%d]: %w", name, i, err)
			}
			if r.Fallback && !cfg.UploadImages {
				slog.Warn("fallback image rewrite has no effect without upload_images", "job", name, "rule", i)
			}
			rewrites = append(rewrites, rss.ImageRewrite{Match: re, Replace: r.Replace, Fallback: r.Fallback})
		}

		// batched messages are plain text, so embed renders the URL there
		embeds := rss.NewEmbedCollector(cfg.SenderMode == config.SenderModeBatched)
		funcs := templateFuncs(embeds)
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
			var uploader *rss.ImageUploader
			if cfg.UploadImages {
				uploader = rss.NewImageUploader(cfg.ImageReferer, config.DefaultFetchTimeout, rewrites)
			}
			sender = rss.NewInstantSender(router, jobURLs[name], tmpl, embeds, uploader)
		case config.SenderModeBatched:
			if cfg.UploadImages {
				logger.Warn("upload_images has no effect in batched sender mode")
			}
			sender = rss.NewBatchedSender(router, jobURLs[name], tmpl, embeds)
		}

		fetchTimeout := cfg.FetchTimeout
		if fetchTimeout <= 0 {
			// 60s
			fetchTimeout = config.DefaultFetchTimeout
		}

		opts := rss.JobOptions{
			TitlePrefix:   prefix,
			RequireImage:  cfg.RequireImage,
			HideTitle:     cfg.HideTitle,
			FetchTimeout:  fetchTimeout,
			Backfill:      cfg.Backfill,
			ImageRewrites: rewrites,
		}

		var rssJob rss.FeedPoller
		switch cfg.ChangeMode {
		case config.ChangeModeGUID:
			rssJob = rss.NewGUIDJob(*logger, *url, opts, sender)
		case config.ChangeModePubDate:
			rssJob = rss.NewPubDateJob(*logger, *url, opts, cfg.SendFutureArticles, sender)
		}

		state := newStateFile(c.StateDir, name, cfg.Source, logger)
		// closed once the job has state to work from; runs before that are
		// skipped so they can't race the initial fetch
		ready := make(chan struct{})
		if state.load(rssJob) {
			restored++
			close(ready)
		} else {
			inits = append(inits, func() bool {
				defer close(ready)
				// A feed that fails at startup (timeout, 4xx/5xx, bad XML, DNS...) must not
				// take the whole daemon down: the job seeds its state on the first
				// successful scheduled fetch instead
				start := time.Now()
				if err := rssJob.Init(ctx); err != nil {
					logger.Warn("Initial fetch failed, will seed on the first successful scheduled run", "err", err, "took", time.Since(start).Round(time.Millisecond))
					return false
				}
				logger.Info("Initial fetch done, feed seeded without sending", "took", time.Since(start).Round(time.Millisecond))
				state.save(rssJob)
				return true
			})
		}

		schedule := hashSchedule(name, cfg.Schedule)
		// format: * * * * *   -> without seconds (5 elements)
		//         * * * * * * -> with seconds (6 elements)
		scheduleSplit := strings.Split(schedule, " ")
		if strings.HasPrefix(scheduleSplit[0], "TZ=") || strings.HasPrefix(scheduleSplit[0], "CRON_TZ=") {
			scheduleSplit = scheduleSplit[1:]
		}
		withSeconds := len(scheduleSplit) > 5

		j, err := s.NewJob(
			gocron.CronJob(schedule, withSeconds),
			gocron.NewTask(func(contxt context.Context) error {
				select {
				case <-ready:
				default:
					logger.Info("Skipping run, initial fetch still pending")
					return nil
				}
				err := rssJob.RetrieveAndSendNewItems(contxt)
				state.save(rssJob)
				return err
			}),
			gocron.WithName(name),
			gocron.WithContext(ctx),
			// skip a run while the previous one is still sending, else both
			// see the same items as new and send them twice
			gocron.WithSingletonMode(gocron.LimitModeReschedule),
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

		if schedule != cfg.Schedule {
			logger.Info("Added cronjob to scheduler", "id", j.ID(), "schedule", cfg.Schedule, "resolved", schedule)
		} else {
			logger.Info("Added cronjob to scheduler", "id", j.ID(), "schedule", schedule)
		}
	}

	slog.Info("Jobs set up", "jobs", len(c.Jobs), "restored", restored, "to_seed", len(inits))
	// initial fetches run in the background so a slow feed doesn't hold up
	// the scheduler; each job skips its runs until its own fetch is done
	if len(inits) > 0 {
		go func() {
			start := time.Now()
			ok := runAll(inits, int(c.MaxConcurrentJobs))
			slog.Info("Initial fetches finished", "ok", ok, "failed", len(inits)-ok, "took", time.Since(start).Round(time.Second))
		}()
	}
	return s, nil
}

// StopTimeout is how long shutdown waits for running jobs
const StopTimeout = 60 * time.Second

// runAll runs fns with at most limit at once and returns how many returned
// true; limit <= 0 means no limit
func runAll(fns []func() bool, limit int) int {
	if limit <= 0 {
		limit = max(len(fns), 1)
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var ok atomic.Int64
	for _, fn := range fns {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			if fn() {
				ok.Add(1)
			}
		})
	}
	wg.Wait()
	return int(ok.Load())
}

// cronRanges are the value ranges of the 6 cron fields, seconds first
var cronRanges = [6][2]int{{0, 59}, {0, 59}, {0, 23}, {1, 28}, {1, 12}, {0, 6}}

// hashSchedule replaces H fields with a value derived from the job name, and
// H/n with a step of n starting at such a value, so jobs spread out evenly
// but each keeps the same times across restarts
func hashSchedule(name, schedule string) string {
	fields := strings.Fields(schedule)
	var prefix []string
	if len(fields) > 0 && (strings.HasPrefix(fields[0], "TZ=") || strings.HasPrefix(fields[0], "CRON_TZ=")) {
		prefix, fields = fields[:1], fields[1:]
	}
	ranges := cronRanges[:]
	if len(fields) == 5 {
		ranges = ranges[1:]
	}
	for i, f := range fields {
		if i >= len(ranges) || (f != "H" && !strings.HasPrefix(f, "H/")) {
			continue
		}
		lo, hi := ranges[i][0], ranges[i][1]
		span := hi - lo + 1
		step := 0
		if f != "H" {
			n, err := strconv.Atoi(f[2:])
			if err != nil || n <= 0 {
				continue
			}
			step, span = n, min(n, span)
		}
		// sha256 so each field's value is independent of the others; a weak
		// hash (fnv) made second and minute nearly equal, clumping jobs
		h := sha256.Sum256([]byte(name + "/" + strconv.Itoa(i)))
		fields[i] = strconv.Itoa(lo + int(binary.BigEndian.Uint64(h[:8])%uint64(span)))
		if step > 0 {
			fields[i] += "/" + strconv.Itoa(step)
		}
	}
	return strings.Join(append(prefix, fields...), " ")
}

// templateFuncs returns the functions available in msg_template.
func templateFuncs(embeds *rss.EmbedCollector) map[string]any {
	return map[string]any{
		"html2text": html2text.HTML2Text,
		"embed":     embeds.Embed,
		"replace":   strings.ReplaceAll,
	}
}
