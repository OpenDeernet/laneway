package jira

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
)

// SearchCards returns the cards a JQL search finds, up to the card limit,
// paging by the enhanced search's nextPageToken. Points come from the first
// story point field an issue fills.
func (c *Client) SearchCards(ctx context.Context, jql string) ([]Card, error) {
	cards, _, err := c.searchCards(ctx, jql)
	return cards, err
}

// SearchCardsTotal is SearchCards with how many issues jql finds: past the
// card limit by Jira's approximate count, else the cards'.
func (c *Client) SearchCardsTotal(ctx context.Context, jql string) ([]Card, int, error) {
	cards, cut, err := c.searchCards(ctx, jql)
	total := len(cards)
	if cut && err == nil {
		if n, cerr := c.Count(ctx, jql); cerr == nil {
			total = max(n, total)
		}
	}
	return cards, total, err
}

// searchCards is SearchCards, saying whether the card limit cut it off.
func (c *Client) searchCards(ctx context.Context, jql string) ([]Card, bool, error) {
	if !c.Enabled() {
		return nil, false, errNotConfigured
	}
	sp := c.resolveStoryPointFields(ctx)
	fields := append(strings.Split(cardFields, ","), sp...)
	dev, flag := c.devField(ctx), c.flagField(ctx)
	more := c.moreCardFields(ctx)
	for _, id := range append([]string{dev, flag}, more.ids()...) {
		if id != "" {
			fields = append(fields, id)
		}
	}
	issues, cut, err := c.searchUpTo(ctx, jql, fields, "", c.cardLimit)
	if err != nil {
		return nil, false, err
	}
	out := make([]Card, 0, len(issues))
	for _, is := range issues {
		if !ValidKey(is.Key) {
			continue
		}
		card := toCard(is.Key, is.Fields, "")
		for _, id := range sp {
			if card = toCard(is.Key, is.Fields, id); card.Points != "" {
				break
			}
		}
		card.PR = prState(is.Fields[dev])
		card.Deploy = deployEnv(is.Fields[dev])
		card.Flagged = flagSet(is.Fields[flag])
		more.fill(&card, is.Fields)
		out = append(out, card)
	}
	if c.index != nil {
		c.index.PutCards(out)
	}
	return out, cut, nil
}

// rawIssue is a search hit with its fields undecoded.
type rawIssue struct {
	Key       string                     `json:"key"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Changelog struct {
		Histories []struct {
			Created string `json:"created"`
			Items   []struct {
				Field string `json:"field"`
				From  string `json:"from"`
				To    string `json:"to"`
			} `json:"items"`
		} `json:"histories"`
	} `json:"changelog"` // with expand "changelog" only
}

// search runs jql up to the card limit, paging by the enhanced search's
// nextPageToken.
func (c *Client) search(ctx context.Context, jql string, fields []string) ([]rawIssue, error) {
	return c.searchExpand(ctx, jql, fields, "")
}

// searchExpand is search with expand ("changelog"), "" for none.
func (c *Client) searchExpand(ctx context.Context, jql string, fields []string, expand string) ([]rawIssue, error) {
	out, _, err := c.searchUpTo(ctx, jql, fields, expand, c.cardLimit)
	return out, err
}

// chartLimit bounds the searches charts and the roadmap count from: past
// the card limit, so a big sprint or project counts whole, but bounded, as
// they may read each issue's changelog.
const chartLimit = 5000

// searchChart is searchExpand up to chartLimit (or the card limit, when
// higher).
func (c *Client) searchChart(ctx context.Context, jql string, fields []string, expand string) ([]rawIssue, error) {
	out, _, err := c.searchUpTo(ctx, jql, fields, expand, max(c.cardLimit, chartLimit))
	return out, err
}

// searchAll is search without the card limit, for totals that must be whole.
func (c *Client) searchAll(ctx context.Context, jql string, fields []string) ([]rawIssue, error) {
	out, _, err := c.searchUpTo(ctx, jql, fields, "", math.MaxInt)
	return out, err
}

// searchUpTo pages through jql's issues until it has limit of them; cut is
// whether jql finds issues past them.
func (c *Client) searchUpTo(ctx context.Context, jql string, fields []string, expand string, limit int) (out []rawIssue, cut bool, err error) {
	token := ""
	for len(out) < limit {
		body := map[string]any{"jql": jql, "fields": fields, "maxResults": cardPage}
		if expand != "" {
			body["expand"] = expand
		}
		if token != "" {
			body["nextPageToken"] = token
		}
		var resp struct {
			Issues        []rawIssue `json:"issues"`
			NextPageToken string     `json:"nextPageToken"`
		}
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", "search", body, &resp); err != nil {
			return nil, false, err
		}
		out = append(out, resp.Issues...)
		if token = resp.NextPageToken; token == "" || len(resp.Issues) == 0 {
			break
		}
	}
	// Stopped at the limit with a page left, or past it on the last page.
	cut = len(out) > limit || len(out) == limit && token != ""
	if len(out) > limit {
		out = out[:limit]
	}
	return out, cut, nil
}

// Count is how many issues jql finds, by Jira's approximate count (exact for
// all but recent changes); no issue is read.
func (c *Client) Count(ctx context.Context, jql string) (int, error) {
	if !c.Enabled() {
		return 0, errNotConfigured
	}
	var resp struct {
		Count int `json:"count"`
	}
	err := c.do(ctx, http.MethodPost, "/rest/api/3/search/approximate-count", "count", map[string]string{"jql": jql}, &resp)
	return resp.Count, err
}

// FindIssues is a text search over every issue you can see: summary,
// description and comments, words as typed prefixes; one page of n,
// recently updated first.
func (c *Client) FindIssues(ctx context.Context, text string, n int) ([]Card, error) {
	return c.FindIssuesIn(ctx, text, "", "", n)
}

// FindIssuesIn is FindIssues among the issues JQL within finds ("" for all),
// its open ones when text is empty. A typed key (or a bare number, in
// project) finds that issue alone when it exists and is within.
func (c *Client) FindIssuesIn(ctx context.Context, text, within, project string, n int) ([]Card, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	clean := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' {
			return ' '
		}
		return r
	}, strings.TrimSpace(text))
	scope := func(jql string) string {
		if within == "" {
			return jql
		}
		return "(" + within + ") AND " + jql
	}
	if clean == "" {
		if within == "" {
			return nil, nil
		}
		return c.findPage(ctx, scope("statusCategory != Done"), n) // nothing typed: the open ones
	}
	k := strings.ToUpper(clean)
	number := project != "" && strings.Trim(k, "0123456789") == ""
	if number {
		k = project + "-" + k
	}
	if ValidKey(k) {
		cards, err := c.findPage(ctx, scope("key = "+k), 1)
		if err == nil && len(cards) > 0 || !number {
			return cards, nil // a key that doesn't exist is Jira's 400: nothing found
		}
	}
	var words []string
	for _, w := range strings.Fields(clean) {
		words = append(words, w+"*")
	}
	return c.findPage(ctx, scope(`text ~ "`+strings.Join(words, " ")+`"`), n)
}

// findPage is the first n cards jql finds, recently updated first.
func (c *Client) findPage(ctx context.Context, jql string, n int) ([]Card, error) {
	body := map[string]any{"jql": jql + " ORDER BY updated DESC", "fields": strings.Split(cardFields, ","), "maxResults": n}
	var resp struct {
		Issues []rawIssue `json:"issues"`
	}
	if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", "search", body, &resp); err != nil {
		return nil, err
	}
	out := make([]Card, 0, len(resp.Issues))
	for _, is := range resp.Issues {
		if ValidKey(is.Key) {
			out = append(out, toCard(is.Key, is.Fields, ""))
		}
	}
	return out, nil
}
