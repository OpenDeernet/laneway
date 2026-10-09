package contract

import (
	"context"
	"fmt"
	"time"

	"github.com/cornedor/laneway/internal/jira"
)

// maxIssues is how many issues the probe reads in full, picked for a
// spread of types and statuses.
const maxIssues = 12

func ignore[T any](_ T, err error) error                  { return err }
func ignore2[T, U any](_ T, _ U, err error) error         { return err }
func ignore3[T, U, V any](_ T, _ U, _ V, err error) error { return err }

// Probe makes the app's reads on project: the site, the project, its first
// board and a spread of its issues. A read that fails is listed and the
// rest go on; an answer that names no issue leaves less to read.
func Probe(ctx context.Context, c *jira.Client, project string) []error {
	var errs []error
	try := func(name string, err error) {
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	now := time.Now()
	since := now.AddDate(0, 0, -14)

	try("Myself", ignore(c.Myself(ctx)))
	try("Priorities", ignore(c.Priorities(ctx)))
	try("StatusNames", ignore(c.StatusNames(ctx)))
	try("LinkTypes", ignore(c.LinkTypes(ctx)))
	try("JQLAutocomplete", ignore(c.JQLAutocomplete(ctx)))
	try("JQLValues", ignore(c.JQLValues(ctx, "status", "")))
	try("FavouriteFilters", ignore(c.FavouriteFilters(ctx)))
	try("Projects", ignore(c.Projects(ctx)))
	try("ListProjects", ignore(c.ListProjects(ctx)))
	c.CanSetStart(ctx)
	pf := c.StoryPointsField(ctx)

	types, err := c.IssueTypes(ctx, project)
	try("IssueTypes", err)
	try("SubtaskTypes", ignore(c.SubtaskTypes(ctx, project)))
	for _, t := range types {
		try("CreateFields", ignore(c.CreateFields(ctx, project, t.Name)))
		try("TransitionRules", ignore(c.TransitionRules(ctx, project, t.ID)))
	}
	try("ProjectStatuses", ignore(c.ProjectStatuses(ctx, project)))
	try("RecentLabels", ignore(c.RecentLabels(ctx, project)))
	try("Versions", ignore(c.Versions(ctx, project)))
	try("ProjectUsers", ignore(c.ProjectUsers(ctx, project)))
	try("CommentVisibilities", ignore(c.CommentVisibilities(ctx, project)))
	try("CycleTimes", ignore(c.CycleTimes(ctx, project, 8, nil, nil)))
	try("Roadmap", ignore(c.Roadmap(ctx, project, "Epic", 30)))
	if ds, err := c.Dashboards(ctx); err != nil {
		try("Dashboards", err)
	} else if len(ds) > 0 {
		gs, err := c.Gadgets(ctx, ds[0].ID)
		try("Gadgets", err)
		for _, g := range gs {
			c.GadgetData(ctx, ds[0].ID, g) // most of Jira's own keep their settings out of reach
		}
	}

	var cards []jira.Card
	boards, err := c.Boards(ctx, project)
	try("Boards", err)
	if len(boards) > 0 {
		b := boards[0].ID
		// Cards carry the board's estimate, as the app asks for them.
		if cfg, err := c.BoardConfiguration(ctx, b); err != nil {
			try("BoardConfiguration", err)
		} else if cfg.PointsField != "" {
			pf = cfg.PointsField
		}
		try("QuickFilters", ignore(c.QuickFilters(ctx, b)))
		try("CardColors", ignore(c.CardColors(ctx, b)))
		some, _, err := c.BoardIssues(ctx, b, "", pf)
		try("BoardIssues", err)
		cards = append(cards, some...)
		some, _, err = c.BacklogIssues(ctx, b, "", pf)
		try("BacklogIssues", err)
		cards = append(cards, some...)
		sprints, err := c.Sprints(ctx, b)
		try("Sprints", err)
		for _, s := range sprints {
			some, _, err = c.SprintIssues(ctx, b, s.ID, "", pf)
			try("SprintIssues", err)
			cards = append(cards, some...)
			if s.State == "active" {
				try("SprintBurn", ignore(c.SprintBurn(ctx, s.ID, pf)))
			}
		}
		try("ClosedSprints", ignore(c.ClosedSprints(ctx, b)))
		try("Velocity", ignore(c.Velocity(ctx, b, 4, pf)))
		try("Retro", ignore(c.Retro(ctx, b, 3, pf, nil, nil)))
	}

	jql := fmt.Sprintf("project = %s ORDER BY updated DESC", project)
	found, err := c.SearchCards(ctx, jql)
	try("SearchCards", err)
	cards = append(cards, found...)
	try("Count", ignore(c.Count(ctx, jql)))
	try("FindIssues", ignore(c.FindIssues(ctx, "the", 5)))
	try("InboxIssues", ignore(c.InboxIssues(ctx, since)))
	try("Standup", ignore(c.Standup(ctx, since)))
	try("MyWorklogsBetween", ignore(c.MyWorklogsBetween(ctx, since, now)))

	keys := pick(cards)
	try("Blockers", ignore(c.Blockers(ctx, keys)))
	try("StatusMoves", ignore(c.StatusMoves(ctx, keys)))
	for _, k := range keys {
		try("Get "+k, ignore(c.Get(ctx, k)))
		try("EditMeta "+k, ignore2(c.EditMeta(ctx, k)))
		try("Transitions "+k, ignore(c.Transitions(ctx, k)))
		try("TransitionsMeta "+k, ignore(c.TransitionsMeta(ctx, k)))
		try("IssueContext "+k, ignore(c.IssueContext(ctx, k)))
		try("Changelog "+k, ignore(c.Changelog(ctx, k)))
		try("History "+k, ignore(c.History(ctx, k)))
		try("IssueInbox "+k, ignore(c.IssueInbox(ctx, k, "", since)))
		try("Watchers "+k, ignore(c.Watchers(ctx, k)))
		try("WebLinks "+k, ignore(c.WebLinks(ctx, k)))
		try("Children "+k, ignore(c.Children(ctx, k)))
		try("DevInfo "+k, ignore(c.DevInfo(ctx, k)))
		try("IssueWorklogs "+k, ignore(c.IssueWorklogs(ctx, k)))
		try("Dependencies "+k, ignore3(c.Dependencies(ctx, k)))
		try("TimeInStatus "+k, ignore(c.TimeInStatus(ctx, k, now)))
		try("Description "+k, ignore(c.Description(ctx, k)))
		try("Flagged "+k, ignore(c.Flagged(ctx, k)))
		try("AssignableUsers "+k, ignore(c.AssignableUsers(ctx, k, "")))
		try("ViewUsers "+k, ignore(c.ViewUsers(ctx, k, "")))
		try("ChangedSince "+k, ignore(c.ChangedSince(ctx, k, since)))
	}

	// Jira's errors have a shape too.
	_, err = c.Get(ctx, project+"-999999")
	try("Get unknown (want an error)", expectErr(err))
	_, err = c.SearchCards(ctx, "status = = nonsense")
	try("SearchCards bad JQL (want an error)", expectErr(err))
	return errs
}

func expectErr(err error) error {
	if err == nil {
		return fmt.Errorf("no error")
	}
	return nil
}

// pick takes up to maxIssues keys from cards, a new type or status first,
// then the parents they name.
func pick(cards []jira.Card) []string {
	var keys []string
	seen := map[string]bool{}
	combo := map[string]bool{}
	for _, c := range cards {
		if len(keys) == maxIssues {
			break
		}
		if seen[c.Key] || combo[c.Type+"/"+c.Status] {
			continue
		}
		seen[c.Key], combo[c.Type+"/"+c.Status] = true, true
		keys = append(keys, c.Key)
	}
	for _, c := range cards {
		if len(keys) == maxIssues {
			break
		}
		if p := c.ParentKey; p != "" && !seen[p] {
			seen[p] = true
			keys = append(keys, p)
		}
	}
	return keys
}
