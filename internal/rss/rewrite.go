package rss

import "regexp"

// ImageRewrite rewrites matching image URLs. Fallback rules aren't applied up
// front, only when an image can't be attached (see ImageUploader)
type ImageRewrite struct {
	Match    *regexp.Regexp
	Replace  string // regexp.Expand syntax: $1, ${name}
	Fallback bool
}

// rewriteImages applies every non-fallback rule, in order, to each URL
func rewriteImages(rules []ImageRewrite, images []string) []string {
	if len(rules) == 0 || len(images) == 0 {
		return images
	}
	out := make([]string, len(images))
	for i, img := range images {
		for _, r := range rules {
			if !r.Fallback && r.Match.MatchString(img) {
				img = r.Match.ReplaceAllString(img, r.Replace)
			}
		}
		out[i] = img
	}
	return out
}

// fallbackImages returns the URLs the fallback rules give for img, in order
func fallbackImages(rules []ImageRewrite, img string) []string {
	var out []string
	for _, r := range rules {
		if r.Fallback && r.Match.MatchString(img) {
			if alt := r.Match.ReplaceAllString(img, r.Replace); alt != img {
				out = append(out, alt)
			}
		}
	}
	return out
}
