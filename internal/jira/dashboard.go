package jira

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Dashboards and their gadgets, as Jira lists them. What a gadget shows
// comes from laneway-server, which computes it as the viewer sees it;
// against Jira only a gadget that lists a filter's or a JQL's issues is
// drawn here (from its config property), the rest open in Jira.

// Dashboard is one dashboard the person may see.
type Dashboard struct {
	ID        string
	Name      string
	Owner     string
	Favourite bool
	View      string // its page on the site
}

// Gadget is one gadget on a dashboard, at its row and column.
type Gadget struct {
	ID        int
	ModuleKey string
	URI       string
	Title     string
	Color     string
	Row, Col  int
}

// Kind is the gadget's kind without its plugin prefix
// ("filter-results-gadget"), from the module key or Jira's URI.
func (g Gadget) Kind() string {
	k := g.ModuleKey
	if k == "" {
		// Jira lists some by URI only: rest/gadgets/1.0/g/<plugin>:<kind>/….
		if _, rest, ok := strings.Cut(g.URI, "/g/"); ok {
			k, _, _ = strings.Cut(rest, "/")
		}
	}
	if _, kind, ok := strings.Cut(k, ":"); ok {
		return kind
	}
	return k
}

// Dashboards are the person's favourite dashboards first, then the others
// they may see, each by name.
func (c *Client) Dashboards(ctx context.Context) ([]Dashboard, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var out []Dashboard
	for start := 0; start < 1000; {
		var page struct {
			Values []struct {
				ID          string `json:"id"`
				Name        string `json:"name"`
				View        string `json:"view"`
				IsFavourite bool   `json:"isFavourite"`
				Owner       *user  `json:"owner"`
			} `json:"values"`
			IsLast bool `json:"isLast"`
		}
		path := "/rest/api/3/dashboard/search?expand=owner,viewUrl,favourite&orderBy=name&maxResults=100&startAt=" + strconv.Itoa(start)
		if err := c.do(ctx, http.MethodGet, path, "dashboards", nil, &page); err != nil {
			return nil, err
		}
		for _, v := range page.Values {
			d := Dashboard{ID: v.ID, Name: v.Name, Favourite: v.IsFavourite, View: v.View}
			if v.Owner != nil {
				d.Owner = v.Owner.DisplayName
			}
			out = append(out, d)
		}
		if page.IsLast || len(page.Values) == 0 {
			break
		}
		start += len(page.Values)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Favourite && !out[j].Favourite })
	return out, nil
}

// Gadgets are a dashboard's gadgets, by column and row.
func (c *Client) Gadgets(ctx context.Context, dashboard string) ([]Gadget, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	var resp struct {
		Gadgets []struct {
			ID        int    `json:"id"`
			ModuleKey string `json:"moduleKey"`
			URI       string `json:"uri"`
			Title     string `json:"title"`
			Color     string `json:"color"`
			Position  struct {
				Row    int `json:"row"`
				Column int `json:"column"`
			} `json:"position"`
		} `json:"gadgets"`
	}
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/dashboard/"+url.PathEscape(dashboard)+"/gadget", "dashboard "+dashboard, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]Gadget, 0, len(resp.Gadgets))
	for _, g := range resp.Gadgets {
		out = append(out, Gadget{ID: g.ID, ModuleKey: g.ModuleKey, URI: g.URI, Title: g.Title, Color: g.Color, Row: g.Position.Row, Col: g.Position.Column})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Col != out[j].Col {
			return out[i].Col < out[j].Col
		}
		return out[i].Row < out[j].Row
	})
	return out, nil
}

// ErrGadgetElsewhere is a gadget this app can't draw on the site: open
// the dashboard there.
var ErrGadgetElsewhere = errors.New("jira: this gadget shows on the site only")

// GadgetData is what a gadget shows, as laneway-server answers it (its
// shape goes by the gadget's kind). Against Jira a filter or JQL gadget
// gets {jql, total, issues} from its config property; others
// ErrGadgetElsewhere.
func (c *Client) GadgetData(ctx context.Context, dashboard string, g Gadget) (json.RawMessage, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	if is, err := c.Laneway(ctx); err != nil {
		return nil, err
	} else if is {
		raw, err := c.doRaw(ctx, http.MethodGet, "/rest/laneway/1/dashboard/"+url.PathEscape(dashboard)+"/gadget/"+strconv.Itoa(g.ID)+"/data", "gadget "+g.Title, nil)
		return json.RawMessage(raw), err
	}
	jql := ""
	switch g.Kind() {
	case "assigned-to-me-gadget":
		jql = "assignee = currentUser() AND resolution = Unresolved ORDER BY priority DESC, updated DESC"
	case "filter-results-gadget":
		var prop struct {
			Value map[string]any `json:"value"`
		}
		path := "/rest/api/3/dashboard/" + url.PathEscape(dashboard) + "/items/" + strconv.Itoa(g.ID) + "/properties/config"
		if err := c.do(ctx, http.MethodGet, path, "gadget "+g.Title, nil, &prop); err != nil {
			return nil, ErrGadgetElsewhere // Jira's own gadgets keep their settings out of reach
		}
		jql = gadgetJQL(prop.Value)
	}
	if jql == "" {
		return nil, ErrGadgetElsewhere
	}
	cards, total, err := c.SearchCardsTotal(ctx, jql)
	if err != nil {
		return nil, err
	}
	rows := make([]map[string]string, 0, min(len(cards), 20))
	for _, cd := range cards[:min(len(cards), 20)] {
		rows = append(rows, map[string]string{"key": cd.Key, "summary": cd.Summary, "status": cd.Status, "issuetype": cd.Type, "priority": cd.Priority, "assignee": cd.Assignee})
	}
	return json.Marshal(map[string]any{"jql": jql, "total": total, "issues": rows})
}

// gadgetJQL is a gadget config's issues as JQL: its jql, or its filter
// (filterId, as filter-<id> too), "" for neither.
func gadgetJQL(cfg map[string]any) string {
	if s, _ := cfg["jql"].(string); strings.TrimSpace(s) != "" {
		return s
	}
	id := ""
	switch v := cfg["filterId"].(type) {
	case string:
		id = strings.TrimPrefix(v, "filter-")
	case float64:
		id = strconv.Itoa(int(v))
	}
	if _, err := strconv.Atoi(id); err != nil {
		return ""
	}
	return "filter = " + id
}
