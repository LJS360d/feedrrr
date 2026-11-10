package rss

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/nicholas-fedor/shoutrrr/pkg/types"
)

// UploadLimit is the most image data attached to one message
const UploadLimit = 10 << 20

// ImageUploader downloads embed images to attach them instead of linking, for
// hosts the sink can't fetch from itself
type ImageUploader struct {
	client  *http.Client
	referer string
	limit   int
	// Fallback rules give alternatives for images that can't be attached
	rules []ImageRewrite
}

func NewImageUploader(referer string, timeout time.Duration, rules []ImageRewrite) *ImageUploader {
	return &ImageUploader{client: &http.Client{Timeout: timeout}, referer: referer, limit: UploadLimit, rules: rules}
}

// prepare downloads images within the upload limit and returns the embed URLs
// (attachment://name) and files to attach. Each image gets an equal share of
// the limit plus what earlier ones left unused; one over its share or failing
// is retried with fallback rules, then dropped
func (u *ImageUploader) prepare(ctx context.Context, images []string) ([]string, []*types.File) {
	urls := make([]string, 0, len(images))
	files := make([]*types.File, 0, len(images))
	if len(images) == 0 {
		return urls, files
	}
	share := u.limit / len(images)
	spare := 0
	for i, img := range images {
		max := share + spare
		data, ext, err := u.fetch(ctx, img, max)
		for _, alt := range fallbackImages(u.rules, img) {
			if err == nil {
				break
			}
			slog.Debug("Image not attachable, trying fallback", "url", img, "fallback", alt, "err", err)
			data, ext, err = u.fetch(ctx, alt, max)
		}
		if err != nil {
			slog.Warn("Could not attach image, leaving it out", "url", img, "err", err)
			spare = max
			continue
		}
		spare = max - len(data)
		name := fmt.Sprintf("image%d%s", i, ext)
		urls = append(urls, "attachment://"+name)
		files = append(files, &types.File{Name: name, Data: data})
	}
	return urls, files
}

// fetch downloads one image of at most max bytes
func (u *ImageUploader) fetch(ctx context.Context, url string, max int) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	if u.referer != "" {
		req.Header.Set("Referer", u.referer)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > int64(max) {
		return nil, "", fmt.Errorf("%d bytes, only %d left under the upload limit", resp.ContentLength, max)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > max {
		return nil, "", fmt.Errorf("more than the %d bytes left under the upload limit", max)
	}
	return data, imageExt(resp.Header.Get("Content-Type"), url), nil
}

// imageExt picks the attachment's extension from the content type, else the
// URL path
func imageExt(contentType, url string) string {
	if mt, _, err := mime.ParseMediaType(contentType); err == nil {
		switch mt {
		case "image/jpeg":
			return ".jpg"
		case "image/png":
			return ".png"
		case "image/gif":
			return ".gif"
		case "image/webp":
			return ".webp"
		}
	}
	if ext := strings.ToLower(path.Ext(strings.SplitN(url, "?", 2)[0])); ext != "" {
		return ext
	}
	return ".jpg"
}
