package jira

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// CardColors is how a board colours its cards, read from the board's
// settings (the undocumented greenhopper edit model Jira's own board
// settings page uses): by priority, issue type, assignee or JQL.
type CardColors struct {
	By     string      // "priority", "issuetype", "assignee", "custom", or "" for none
	Colors []CardColor // in the board's order; for custom the first match wins
}

// CardColor is one rule: a priority, type or assignee name, or a JQL
// clause, and its colour (#rrggbb).
type CardColor struct {
	Value, Color string
}

// CardColors reads board's card colours; a board without any, or an
// instance that won't say, colours nothing.
func (c *Client) CardColors(ctx context.Context, board int) (CardColors, error) {
	if !c.Enabled() {
		return CardColors{}, errNotConfigured
	}
	if c.atl != nil {
		return CardColors{}, nil // greenhopper is private: Atlassian keeps apps out
	}
	var model struct {
		CardColorConfig struct {
			Strategy string `json:"cardColorStrategy"`
		} `json:"cardColorConfig"`
	}
	path := "/rest/greenhopper/1.0/rapidviewconfig/editmodel.json?rapidViewId=" + strconv.Itoa(board)
	if err := c.do(ctx, http.MethodGet, path, "card colours", nil, &model); err != nil {
		return CardColors{}, err
	}
	strategy := model.CardColorConfig.Strategy
	cc := CardColors{By: strings.ToLower(strategy)}
	if cc.By == "" || cc.By == "none" {
		return CardColors{}, nil
	}
	// The edit model names the strategy only; its colours come apart.
	var resp struct {
		Colors []struct {
			Value        string `json:"value"`
			DisplayValue string `json:"displayValue"`
			Color        string `json:"color"`
		} `json:"cardColors"`
	}
	path = "/rest/greenhopper/1.0/cardcolors/" + strconv.Itoa(board) + "/strategy/" + url.PathEscape(strategy)
	if err := c.do(ctx, http.MethodGet, path, "card colours", nil, &resp); err != nil {
		return CardColors{}, err
	}
	for _, col := range resp.Colors {
		if col.Color == "" {
			continue
		}
		v := col.Value
		if cc.By != "custom" && col.DisplayValue != "" {
			v = col.DisplayValue // a name, as cards carry it
		}
		cc.Colors = append(cc.Colors, CardColor{Value: v, Color: col.Color})
	}
	return cc, nil
}

// CardColorKeys finds which issues a custom colour's JQL takes, within
// scope (the board's own clause, "" for none): key → colour, the first
// rule that matches an issue winning.
func (c *Client) CardColorKeys(ctx context.Context, cc CardColors, scope string) (map[string]string, error) {
	out := map[string]string{}
	for _, col := range cc.Colors {
		jql := "(" + col.Value + ")"
		if scope != "" {
			jql = "(" + scope + ") AND " + jql
		}
		issues, err := c.search(ctx, jql, []string{"key"})
		if err != nil {
			return nil, err
		}
		for _, is := range issues {
			if _, ok := out[is.Key]; !ok {
				out[is.Key] = col.Color
			}
		}
	}
	return out, nil
}
