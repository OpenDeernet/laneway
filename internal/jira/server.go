package jira

import (
	"context"
	"encoding/json"
	"net/http"
)

// siteInfo is what serverInfo says of the site's flavour.
type siteInfo struct {
	Laneway bool `json:"laneway"`
	// SelfHosted is Jira Server or Data Center (deploymentType), which
	// lacks Cloud's bulk worklog move.
	SelfHosted bool `json:"selfHosted,omitempty"`
}

// siteInfoMeta is where the last answer is kept (SetLayouts' store, one
// per site), for a start while the site can't be reached.
const siteInfoMeta = "serverInfo"

// Laneway reports whether the site is laneway-server rather than Jira:
// its serverInfo carries lanewayServerVersion. Features only laneway has
// ask this instead of probing their own endpoint.
func (c *Client) Laneway(ctx context.Context) (bool, error) {
	i, err := c.siteInfo(ctx)
	return i.Laneway, err
}

// siteInfo is read once per client and kept; while it can't be read
// (offline, Jira down) the kept one answers, and it is asked again next
// time.
func (c *Client) siteInfo(ctx context.Context) (siteInfo, error) {
	if !c.Enabled() {
		return siteInfo{}, errNotConfigured
	}
	c.mu.Lock()
	if c.site != nil {
		v := *c.site
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	v, err, _ := c.shared.Do("serverInfo", func() (any, error) {
		var info struct {
			Version    string `json:"lanewayServerVersion"`
			Deployment string `json:"deploymentType"`
		}
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/serverInfo", "server info", nil, &info); err != nil {
			var kept siteInfo
			if c.layouts != nil {
				if raw, ok, _ := c.layouts.GetMeta(siteInfoMeta); ok && json.Unmarshal([]byte(raw), &kept) == nil {
					return kept, nil
				}
			}
			return kept, err
		}
		i := siteInfo{Laneway: info.Version != "", SelfHosted: info.Deployment == "Server" || info.Deployment == "DataCenter"}
		c.mu.Lock()
		c.site = &i
		c.mu.Unlock()
		if c.layouts != nil {
			b, _ := json.Marshal(i)
			_ = c.layouts.SetMeta(siteInfoMeta, string(b))
		}
		return i, nil
	})
	return v.(siteInfo), err
}
