package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// The commands for scripts and CI: list, view, create and move, as plain
// text, CSV or JSON, without the board.

// defaultListJQL is list's query without -jql: your open issues.
const defaultListJQL = "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"

// cliCard is a listed issue as JSON and CSV write it.
type cliCard struct {
	Key      string `json:"key"`
	Summary  string `json:"summary"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Assignee string `json:"assignee"`
	Priority string `json:"priority"`
	Points   string `json:"points"`
	Parent   string `json:"parent"`
}

// cliIssue is a viewed issue as JSON writes it.
type cliIssue struct {
	Key         string       `json:"key"`
	Summary     string       `json:"summary"`
	Type        string       `json:"type"`
	Status      string       `json:"status"`
	Priority    string       `json:"priority"`
	Assignee    string       `json:"assignee"`
	Reporter    string       `json:"reporter"`
	Labels      []string     `json:"labels"`
	Points      string       `json:"points"`
	Updated     time.Time    `json:"updated"`
	URL         string       `json:"url"`
	Description string       `json:"description"`
	Comments    []cliComment `json:"comments"`
}

type cliComment struct {
	Author string `json:"author"`
	Body   string `json:"body"`
}

// cliCmd runs one of list, view, create and move.
func cliCmd(name string, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(errOut)
	cfgPath := fs.String("config", "", i18n.T("config file"))
	site := fs.String("site", "", i18n.T("Jira site from the config's sites:"))
	format := fs.String("format", "plain", i18n.T("plain, csv or json"))
	jql := fs.String("jql", defaultListJQL, i18n.T("list: the query"))
	project := fs.String("project", "", i18n.T("create: the project key"))
	typ := fs.String("type", "Task", i18n.T("create: the issue type"))
	summary := fs.String("summary", "", i18n.T("create: the summary"))
	desc := fs.String("description", "", i18n.T("create: the description (markdown)"))
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *format != "plain" && *format != "csv" && *format != "json" {
		fmt.Fprintf(errOut, i18n.T("laneway %s: -format is plain, csv or json")+"\n", name)
		return 2
	}
	c, err := siteClient(*cfgPath, *site)
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	switch name {
	case "list":
		err = cliList(ctx, c, *jql, *format, out)
	case "view":
		if fs.NArg() != 1 {
			fmt.Fprintln(errOut, i18n.T("usage: laneway view [-format plain|json] KEY"))
			return 2
		}
		err = cliView(ctx, c, fs.Arg(0), *format, out)
	case "create":
		if *project == "" || *summary == "" {
			fmt.Fprintln(errOut, i18n.T("usage: laneway create -project ABC [-type Task] -summary TEXT [-description MD]"))
			return 2
		}
		var key string
		key, err = c.CreateIssue(ctx, jira.NewIssue{Project: *project, Type: *typ, Summary: *summary, Description: *desc})
		if err == nil {
			err = cliWrite(out, *format, map[string]string{"key": key, "url": c.BrowseURL(key)}, key)
		}
	case "move":
		switch fs.NArg() {
		case 1: // where it can go
			err = cliMoves(ctx, c, fs.Arg(0), out)
		case 2:
			err = cliMove(ctx, c, fs.Arg(0), fs.Arg(1), out)
		default:
			fmt.Fprintln(errOut, i18n.T("usage: laneway move KEY [STATUS]"))
			return 2
		}
	}
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	return 0
}

func cliList(ctx context.Context, c *jira.Client, jql, format string, out io.Writer) error {
	cards, err := c.SearchCards(ctx, jql)
	if err != nil {
		return err
	}
	rows := make([]cliCard, len(cards))
	for i, cd := range cards {
		rows[i] = cliCard{cd.Key, cd.Summary, cd.Status, cd.Type, cd.Assignee, cd.Priority, cd.Points, cd.ParentKey}
	}
	switch format {
	case "json":
		return cliJSON(out, rows)
	case "csv":
		w := csv.NewWriter(out)
		_ = w.Write([]string{"key", "summary", "status", "type", "assignee", "priority", "points", "parent"})
		for _, r := range rows {
			_ = w.Write([]string{r.Key, r.Summary, r.Status, r.Type, r.Assignee, r.Priority, r.Points, r.Parent})
		}
		w.Flush()
		return w.Error()
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Key, r.Status, orNone(r.Assignee), r.Summary)
	}
	return tw.Flush()
}

func orNone(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func cliView(ctx context.Context, c *jira.Client, key, format string, out io.Writer) error {
	is, err := c.Get(ctx, key)
	if err != nil {
		return err
	}
	v := cliIssue{Key: is.Key, Summary: is.Summary, Type: is.Type, Status: is.Status, Priority: is.Priority, Assignee: is.Assignee,
		Reporter: is.Reporter, Labels: is.Labels, Points: is.StoryPoints, Updated: is.Updated, URL: is.URL, Description: is.Description}
	for _, cm := range is.Comments {
		v.Comments = append(v.Comments, cliComment{cm.Author, cm.Body})
	}
	switch format {
	case "json":
		return cliJSON(out, v)
	case "csv":
		return errors.New(i18n.T("view: -format is plain or json"))
	}
	fmt.Fprintf(out, "%s  %s\n\n", v.Key, v.Summary)
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for _, f := range [][2]string{{i18n.T("Type"), v.Type}, {i18n.T("Status"), v.Status}, {i18n.T("Priority"), v.Priority}, {i18n.T("Assignee"), orNone(v.Assignee)},
		{i18n.T("Reporter"), v.Reporter}, {i18n.T("Points"), orNone(v.Points)}, {i18n.T("Labels"), orNone(strings.Join(v.Labels, ", "))}, {"URL", v.URL}} {
		fmt.Fprintf(tw, "%s\t%s\n", f[0], f[1])
	}
	_ = tw.Flush()
	if strings.TrimSpace(v.Description) != "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimSpace(v.Description))
	}
	for _, cm := range v.Comments {
		fmt.Fprintf(out, "\n— %s\n%s\n", cm.Author, strings.TrimSpace(cm.Body))
	}
	return nil
}

// cliMove moves key along the transition to status, by name.
func cliMove(ctx context.Context, c *jira.Client, key, status string, out io.Writer) error {
	ts, err := c.Transitions(ctx, key)
	if err != nil {
		return err
	}
	var names []string
	for _, t := range ts {
		if strings.EqualFold(t.Name, status) {
			if err := c.DoTransition(ctx, key, t.ID); err != nil {
				return err
			}
			fmt.Fprintf(out, "%s → %s\n", key, t.Name)
			return nil
		}
		names = append(names, t.Name)
	}
	return fmt.Errorf(i18n.T("%s can't move to %q; it can to %s"), key, status, strings.Join(names, ", "))
}

// cliMoves lists the statuses an issue can move to, one a line.
func cliMoves(ctx context.Context, c *jira.Client, key string, out io.Writer) error {
	ts, err := c.Transitions(ctx, key)
	if err != nil {
		return err
	}
	for _, t := range ts {
		fmt.Fprintln(out, t.Name)
	}
	return nil
}

// cliWrite writes v as JSON, else plain.
func cliWrite(out io.Writer, format string, v any, plain string) error {
	if format == "json" {
		return cliJSON(out, v)
	}
	_, err := fmt.Fprintln(out, plain)
	return err
}

func cliJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
