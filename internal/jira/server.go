package jira

import (
	"context"
	"net/http"
)

// Laneway reports whether the site is laneway-server rather than Jira:
// its serverInfo carries lanewayServerVersion. Features only laneway has
// ask this instead of probing their own endpoint. The answer is read once
// per client; an error (offline, Jira down) is returned and not kept.
func (c *Client) Laneway(ctx context.Context) (bool, error) {
	if !c.Enabled() {
		return false, errNotConfigured
	}
	c.mu.Lock()
	if c.laneway != nil {
		v := *c.laneway
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	v, err, _ := c.shared.Do("serverInfo", func() (any, error) {
		var info struct {
			Version string `json:"lanewayServerVersion"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/serverInfo", "server info", nil, &info); err != nil {
			return false, err
		}
		is := info.Version != ""
		c.mu.Lock()
		c.laneway = &is
		c.mu.Unlock()
		return is, nil
	})
	return v.(bool), err
}
