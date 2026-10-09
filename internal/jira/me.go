package jira

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Your own account on laneway-server: how it mails you, your time zone and
// the language of its mail, the browsers signed in as you, and your
// picture. Jira Cloud keeps these on the Atlassian account, so each is
// laneway-server's only.

// Notifications is how laneway-server mails you: at all, and instant,
// hourly or daily.
type Notifications struct {
	Email bool
	Mode  string
}

// Session is a browser signed in to laneway-server as you.
type Session struct {
	ID, IP, Method             string
	Created, LastSeen, Expires time.Time
}

// Notifications reads how the server mails you. An app's sign-in may not
// (ErrForbidden or ErrUnauthorized) until the server lets it.
func (c *Client) Notifications(ctx context.Context) (Notifications, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return Notifications{}, err
	}
	var resp struct {
		Email bool   `json:"email"`
		Mode  string `json:"mode"`
	}
	err := c.do(ctx, http.MethodGet, "/rest/laneway/1/myself/notifications", "email notifications", nil, &resp)
	return Notifications{resp.Email, resp.Mode}, err
}

// SetNotifications changes how the server mails you.
func (c *Client) SetNotifications(ctx context.Context, n Notifications) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, "/rest/laneway/1/myself/notifications", "email notifications", map[string]any{"email": n.Email, "mode": n.Mode}, nil)
}

// Sessions lists the browsers signed in to the server as you.
func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return nil, err
	}
	var resp []struct {
		ID       string `json:"id"`
		IP       string `json:"ip"`
		Method   string `json:"method"`
		Created  string `json:"created"`
		LastSeen string `json:"lastSeen"`
		Expires  string `json:"expires"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/laneway/1/myself/sessions", "sessions", nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Session, len(resp))
	for i, s := range resp {
		out[i] = Session{ID: s.ID, IP: s.IP, Method: s.Method, Created: parseStamp(s.Created), LastSeen: parseStamp(s.LastSeen), Expires: parseStamp(s.Expires)}
	}
	return out, nil
}

// EndSessions signs you out everywhere: every browser, and every app's
// sign-in, this client's own too when it signed in as an app.
func (c *Client) EndSessions(ctx context.Context) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodDelete, "/rest/laneway/1/myself/sessions", "sessions", nil, nil)
}

// MyTimeZone is your time zone on the server (an IANA name); its digests
// go out by it.
func (c *Client) MyTimeZone(ctx context.Context) (string, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return "", err
	}
	b, err := c.sendRaw(ctx, http.MethodGet, "/rest/api/3/mypreferences?key=jira.user.timezone", "time zone", "", nil)
	return strings.TrimSpace(string(b)), err
}

// SetMyTimeZone sets your time zone (an IANA name).
func (c *Client) SetMyTimeZone(ctx context.Context, tz string) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	_, err := c.sendRaw(ctx, http.MethodPut, "/rest/api/3/mypreferences?key=jira.user.timezone", "time zone", "text/plain", []byte(tz))
	return err
}

// MyLocale is the language the server mails you in (en_US, nl_NL…).
func (c *Client) MyLocale(ctx context.Context) (string, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return "", err
	}
	var resp struct {
		Locale string `json:"locale"`
	}
	err := c.do(ctx, http.MethodGet, "/rest/api/3/mypreferences/locale", "language", nil, &resp)
	return resp.Locale, err
}

// SetMyLocale sets the language the server mails you in.
func (c *Client) SetMyLocale(ctx context.Context, locale string) error {
	if err := c.lanewayOnly(ctx); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, "/rest/api/3/mypreferences/locale", "language", map[string]string{"locale": locale}, nil)
}

// SetMyAvatar makes data (PNG, JPEG or SVG; the server refuses anything
// else, or over 1 MB) your picture. Me asks anew after.
func (c *Client) SetMyAvatar(ctx context.Context, data []byte, contentType string) error {
	me, err := c.avatarOwner(ctx)
	if err != nil {
		return err
	}
	_, err = c.sendRaw(ctx, http.MethodPost, "/rest/api/3/universal_avatar/type/user/owner/"+url.PathEscape(me), "your avatar", contentType, data)
	c.forgetMe()
	return err
}

// MyAvatarUploaded reports whether your picture is one you uploaded.
func (c *Client) MyAvatarUploaded(ctx context.Context) (bool, error) {
	_, ids, err := c.myAvatars(ctx)
	return len(ids) > 0, err
}

// RemoveMyAvatar takes your picture away: the generated one shows again.
func (c *Client) RemoveMyAvatar(ctx context.Context) error {
	owner, ids, err := c.myAvatars(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := c.do(ctx, http.MethodDelete, owner+"/avatar/"+url.PathEscape(id), "your avatar", nil, nil); err != nil {
			return err
		}
	}
	c.forgetMe()
	return nil
}

// myAvatars is the path of your avatars and the ids of those you uploaded.
func (c *Client) myAvatars(ctx context.Context) (string, []string, error) {
	me, err := c.avatarOwner(ctx)
	if err != nil {
		return "", nil, err
	}
	owner := "/rest/api/3/universal_avatar/type/user/owner/" + url.PathEscape(me)
	var list struct {
		Custom []struct {
			ID string `json:"id"`
		} `json:"custom"`
	}
	if err := c.do(ctx, http.MethodGet, owner, "your avatar", nil, &list); err != nil {
		return "", nil, err
	}
	ids := make([]string, len(list.Custom))
	for i, a := range list.Custom {
		ids[i] = a.ID
	}
	return owner, ids, nil
}

// avatarOwner is your account id, on laneway-server only.
func (c *Client) avatarOwner(ctx context.Context) (string, error) {
	if err := c.lanewayOnly(ctx); err != nil {
		return "", err
	}
	me, err := c.Myself(ctx)
	return me.AccountID, err
}

// forgetMe drops the kept Me, so the next asks the server (a new avatar).
func (c *Client) forgetMe() {
	c.mu.Lock()
	c.myself = nil
	c.mu.Unlock()
}

// sendRaw sends body as contentType (none: no body) and returns the raw
// answer: for the calls that take or give plain text or an image rather
// than JSON. Sent once; a write never queues offline.
func (c *Client) sendRaw(ctx context.Context, method, path, what, contentType string, body []byte) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if _, err := c.authorize(req); err != nil {
		return nil, err
	}
	if method != http.MethodGet {
		c.writing.Add(1)
		defer c.writing.Add(-1)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Atlassian-Token", "no-check") // Jira's CSRF guard for uploads
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call jira: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, statusError(resp.StatusCode, what, out, resp.Header.Get("Retry-After"))
	}
	return out, nil
}
