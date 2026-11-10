package rss

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"

	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

type MessageSender interface {
	// Enqueue renders an item and queues it for the next Flush
	Enqueue(title string, item RSSItem) error
	// Flush sends and empties the queue, returning the items not delivered
	Flush() (failed []RSSItem, err error)
}

// Template renders an item; satisfied by both text/template and html/template.
type Template interface {
	Execute(wr io.Writer, data any) error
}

// Router is the part of shoutrrr's *router.ServiceRouter the senders use
type Router interface {
	Send(message string, params *types.Params) []error
	SendItems(items []types.MessageItem, params types.Params) []error
}

type queuedItem struct {
	item   RSSItem
	title  string
	msg    string
	images []string
}

type BaseMessageSender struct {
	tmpl   Template
	embeds *EmbedCollector
	router Router
	params *types.Params
	queue  []queuedItem
}

type BatchedSender struct {
	BaseMessageSender
}

type InstantSender struct {
	BaseMessageSender
	// uploader, if set, attaches embed images as files instead of linking them
	uploader *ImageUploader
}

// NewBatchedSender joins all items of a run into one plain-text message
func NewBatchedSender(router Router, tmpl Template, embeds *EmbedCollector) MessageSender {
	slog.Debug("Initialized new batched sender")
	return &BatchedSender{BaseMessageSender{router: router, tmpl: tmpl, embeds: embeds, params: &types.Params{}}}
}

// NewInstantSender sends every item on its own, with embed images attached.
// uploader, if set, attaches them as files
func NewInstantSender(router Router, tmpl Template, embeds *EmbedCollector, uploader *ImageUploader) MessageSender {
	slog.Debug("Initialized new instant sender", "upload", uploader != nil)
	return &InstantSender{BaseMessageSender{router: router, tmpl: tmpl, embeds: embeds, params: &types.Params{}}, uploader}
}

// imageItems lays out an item with images, attaching them when uploading
func (s *InstantSender) imageItems(msg string, images []string) []types.MessageItem {
	if s.uploader == nil {
		return embedItems(msg, images)
	}
	urls, files := s.uploader.prepare(context.Background(), images)
	if len(urls) == 0 {
		return []types.MessageItem{{Text: msg}}
	}
	items := embedItems(msg, urls)
	for i := range items {
		items[i].File = files[i]
	}
	return items
}

func (b *BaseMessageSender) render(item RSSItem) (string, error) {
	var msgBytes bytes.Buffer
	err := b.tmpl.Execute(&msgBytes, item)
	if err != nil {
		return "", fmt.Errorf("Error encountered while rendering RSS item to message: %w", err)
	}

	return msgBytes.String(), nil
}

func (b *BaseMessageSender) Enqueue(title string, item RSSItem) error {
	b.embeds.reset()
	msg, err := b.render(item)
	if err != nil {
		return err
	}

	b.queue = append(b.queue, queuedItem{item: item, title: title, msg: msg, images: b.embeds.take()})
	return nil
}

// drain empties the queue and returns its items oldest first
func (b *BaseMessageSender) drain() []queuedItem {
	items := b.queue
	b.queue = nil
	slices.Reverse(items)
	return items
}

// Flush sends every queued item as one message; on error all count as failed
func (s *BatchedSender) Flush() ([]RSSItem, error) {
	queued := s.drain()
	if len(queued) == 0 {
		return nil, nil
	}

	msgs := make([]string, len(queued))
	for i, item := range queued {
		msgs[i] = item.msg
	}

	var sendErrs []error
	sendPacer.do(func() {
		sendErrs = s.router.Send(strings.Join(msgs, "\n"), s.params)
	})
	if err := errors.Join(sendErrs...); err != nil {
		failed := make([]RSSItem, len(queued))
		for i, item := range queued {
			failed[i] = item.item
		}
		return failed, fmt.Errorf("Error sending batch of %d items: %w", len(queued), err)
	}

	return nil, nil
}

func (s *InstantSender) Flush() ([]RSSItem, error) {
	var errs []error
	var failed []RSSItem
	succ := 0

	for _, item := range s.drain() {
		s.params.SetTitle(item.title)

		var items []types.MessageItem
		if len(item.images) > 0 {
			// downloads happen outside the pacer so they don't hold up other jobs
			items = s.imageItems(item.msg, item.images)
		}

		var sendErrs []error
		sendPacer.do(func() {
			if len(items) > 0 {
				sendErrs = s.router.SendItems(items, *s.params)
			} else {
				sendErrs = s.router.Send(item.msg, s.params)
			}
		})

		routerErrs := []error{}
		for _, err := range sendErrs {
			if err != nil {
				routerErrs = append(routerErrs, err)
			}
		}
		if len(routerErrs) > 0 {
			name := item.title
			if name == "" {
				name = item.item.key()
			}
			errs = append(errs, fmt.Errorf("Error processing item: '%s': %w", name, errors.Join(routerErrs...)))
			failed = append(failed, item.item)
			continue
		}

		succ += 1
	}

	slog.Debug("Done flushing", "success", succ, "errs", len(errs))

	if len(errs) > 0 {
		return failed, fmt.Errorf("Errors encountered while sending: %w", errors.Join(errs...))
	}

	return nil, nil
}
