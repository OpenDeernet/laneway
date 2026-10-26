package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

func init() {
	// GET /search?jql= runs JQL; {cards, total}, total past the card limit
	// by Jira's approximate count.
	get("/search", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		jql := strings.TrimSpace(Q(r, "jql"))
		if jql == "" {
			return nil, badRequest(i18n.T("jql is empty"))
		}
		cards, total, err := s.Client().SearchCardsTotal(ctx, jql)
		return map[string]any{"cards": cards, "total": total}, err
	})
	// GET /find?q=&n= is the text search over every visible issue; {cards}.
	// A typed key finds that issue, a bare number one in project. With
	// parentOf (an issue key), or type (a new issue's, in project), only the
	// issues that can be its parent.
	get("/find", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		n, _ := strconv.Atoi(Q(r, "n"))
		if n <= 0 || n > 50 {
			n = 20
		}
		c, project, typ, self := s.Client(), strings.ToUpper(Q(r, "project")), Q(r, "type"), strings.ToUpper(Q(r, "parentOf"))
		if self != "" {
			if !jira.ValidKey(self) {
				return nil, badRequest(i18n.T("bad issue key"))
			}
			iss, err := c.Get(ctx, self)
			if err != nil {
				return nil, err
			}
			project, typ = self[:strings.LastIndex(self, "-")], iss.Type
		}
		var within string
		if typ != "" && project != "" {
			var err error
			if within, err = c.ParentJQL(ctx, project, typ); err != nil {
				return nil, err
			}
		}
		cards, err := c.FindIssuesIn(ctx, Q(r, "q"), within, project, n)
		cards = slices.DeleteFunc(cards, func(x jira.Card) bool { return x.Key == self })
		if cards == nil {
			cards = []jira.Card{}
		}
		return map[string]any{"cards": cards}, err
	})
	// Starred searches: views of every board, in the store as the TUI keeps
	// them (jira_tab:jql_saved, a JSON list), so both front ends share them.
	get("/jql/starred", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return starredJQL(s), nil
	})
	post("/jql/starred", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ JQL string }](r)
		if err != nil {
			return nil, err
		}
		q := strings.TrimSpace(b.JQL)
		if q == "" {
			return nil, badRequest(i18n.T("no query"))
		}
		list := starredJQL(s)
		on := !slices.Contains(list, q)
		if on {
			list = append(list, q)
		} else {
			list = slices.DeleteFunc(list, func(x string) bool { return x == q })
		}
		raw, _ := json.Marshal(list)
		return map[string]bool{"On": on}, s.opt.Store.SetMeta(jqlStarredMeta, string(raw))
	})
	// GET /jql/count?jql= is how many issues jql finds, by Jira's approximate
	// count; {Count}, or Jira's complaint about the query.
	get("/jql/count", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		jql := strings.TrimSpace(Q(r, "jql"))
		if jql == "" {
			return nil, badRequest(i18n.T("jql is empty"))
		}
		n, err := s.Client().Count(ctx, jql)
		return map[string]int{"Count": n}, err
	})
	get("/jql/words", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		return s.Client().JQLAutocomplete(ctx)
	})
	get("/jql/values", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		field := Q(r, "field")
		if field == "" {
			return nil, badRequest(i18n.T("field is empty"))
		}
		v, err := s.Client().JQLValues(ctx, field, Q(r, "prefix"))
		if v == nil {
			v = []string{}
		}
		return v, err
	})
	get("/filters", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		f, err := s.Client().FavouriteFilters(ctx)
		if f == nil {
			f = []jira.QuickFilter{}
		}
		return f, err
	})
	post("/filters", func(ctx context.Context, s *Server, r *http.Request) (any, error) {
		b, err := Body[struct{ Name, JQL string }](r)
		if err != nil {
			return nil, err
		}
		b.Name, b.JQL = strings.TrimSpace(b.Name), strings.TrimSpace(b.JQL)
		if b.Name == "" || b.JQL == "" {
			return nil, badRequest(i18n.T("name and jql are required"))
		}
		return nil, s.Client().SaveFilter(ctx, b.Name, b.JQL)
	})
}

const jqlStarredMeta = "jira_tab:jql_saved"

func starredJQL(s *Server) []string {
	out := []string{}
	if v, ok, _ := s.opt.Store.GetMeta(jqlStarredMeta); ok {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return nonNil(out)
}
