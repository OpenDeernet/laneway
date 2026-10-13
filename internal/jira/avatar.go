package jira

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxAvatarBytes caps an avatar download; they are small.
const maxAvatarBytes = 1 << 20

// Avatar downloads an avatar image. The API credentials go only to the
// instance itself, never to another host (Gravatar, Atlassian's avatar
// CDN); Go drops them on a redirect elsewhere too. The URL comes from a
// response, so only the instance and the fixed avatarPrefixes are fetched,
// redirects included: never localhost or a cloud metadata address. The
// request URL is a constant prefix plus a rebuilt, re-escaped path and query.
func (c *Client) Avatar(ctx context.Context, avatarURL string) ([]byte, error) {
	target, own, ok := c.avatarTarget(avatarURL)
	if !ok {
		return nil, fmt.Errorf("avatar: bad url")
	}
	if own && c.atl != nil {
		// The sites' own avatars come through Atlassian's API for them.
		if p, ok := strings.CutPrefix(target, c.wikiURL+"/wiki/"); ok {
			target = c.apiURL("/wiki/" + p)
		} else {
			target = c.apiURL(strings.TrimPrefix(target, c.baseURL))
		}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	if own && c.Enabled() {
		if _, err := c.authorize(req); err != nil {
			return nil, err
		}
	}
	hc := *c.http
	hc.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if _, _, ok := c.avatarTarget(r.URL.String()); len(via) >= 5 || !ok {
			return fmt.Errorf("avatar: redirect to %s refused", r.URL.Host)
		}
		return nil
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("avatar: %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes))
}

// avatarPrefixes are the only external places avatars are fetched from:
// Gravatar, which falls back to Atlassian's initials images, and Atlassian's
// avatar CDN. https only, default port.
var avatarPrefixes = []string{
	"https://secure.gravatar.com/",
	"https://www.gravatar.com/",
	"https://gravatar.com/",
	"https://avatar-management--avatars.us-west-2.prod.public.atl-paas.net/",
	"https://avatar-management--avatars.server-location.prod.public.atl-paas.net/",
	"https://i0.wp.com/",
	"https://i1.wp.com/",
	"https://i2.wp.com/",
}

const maxAvatarURL = 2048

// avatarTarget maps raw onto the instance or one of avatarPrefixes: the
// result is the matching constant prefix plus the path and query of raw
// rebuilt from their parsed, unescaped form. own says it is the instance.
func (c *Client) avatarTarget(raw string) (target string, own, ok bool) {
	if len(raw) > maxAvatarURL {
		return "", false, false
	}
	prefixes := avatarPrefixes
	// Confluence on a site of its own counts as the instance for its
	// pictures.
	own = c.wikiURL != "" && c.wikiURL != c.baseURL && strings.HasPrefix(raw, c.wikiURL+"/wiki/")
	if own {
		prefixes = append([]string{c.wikiURL + "/"}, prefixes...)
	} else if b := strings.TrimRight(c.baseURL, "/"); b != "" {
		prefixes = append([]string{b + "/"}, prefixes...)
	}
	for i, prefix := range prefixes {
		if !strings.HasPrefix(raw, prefix) {
			continue
		}
		rest := raw[len(prefix):]
		rawPath, rawQuery, _ := strings.Cut(rest, "?")
		if strings.Contains(rest, "#") {
			return "", false, false
		}
		var segs []string
		for _, seg := range strings.Split(rawPath, "/") {
			d, err := url.PathUnescape(seg)
			if err != nil {
				return "", false, false
			}
			for _, r := range d {
				if r <= 0x20 || r >= 0x7f || r == '\\' || r == '/' {
					return "", false, false
				}
			}
			if d == "." || d == ".." || d == "" && seg != "" {
				return "", false, false
			}
			segs = append(segs, url.PathEscape(d))
		}
		if strings.Contains(rawPath, "//") {
			return "", false, false
		}
		target = prefix + strings.Join(segs, "/")
		if rawQuery != "" {
			q, err := url.ParseQuery(rawQuery)
			if err != nil {
				return "", false, false
			}
			target += "?" + q.Encode()
		}
		return target, own || i == 0 && strings.TrimRight(c.baseURL, "/") != "", true
	}
	return "", false, false
}
