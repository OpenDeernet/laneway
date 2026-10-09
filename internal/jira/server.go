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

// AdminPage is laneway-server's admin.
func (c *Client) AdminPage() string { return c.baseURL + "/admin" }

// Administers reports whether the account administers the site or a
// project on it: the admin has something for it then.
func (c *Client) Administers(ctx context.Context) (bool, error) {
	if !c.Enabled() {
		return false, errNotConfigured
	}
	var resp struct {
		Permissions map[string]struct {
			Have bool `json:"havePermission"`
		} `json:"permissions"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/mypermissions?permissions=ADMINISTER,ADMINISTER_PROJECTS", "permissions", nil, &resp); err != nil {
		return false, err
	}
	return resp.Permissions["ADMINISTER"].Have || resp.Permissions["ADMINISTER_PROJECTS"].Have, nil
}

// ProfilePage is where Jira keeps your profile: Atlassian's account page
// on Cloud, the site's own on Server and Data Center.
func (c *Client) ProfilePage(ctx context.Context) string {
	if i, _ := c.siteInfo(ctx); i.SelfHosted {
		return c.baseURL + "/secure/ViewProfile.jspa"
	}
	return "https://id.atlassian.com/manage-profile/profile-and-visibility"
}
