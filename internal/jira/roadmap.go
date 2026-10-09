package jira

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The roadmap: a project's epics on a timeline. An epic's dates come from its
// own fields (Start date, or Plans' Target start; Due date, or Target end),
// else from the sprints its children sit in.

// Epic is one roadmap row.
type Epic struct {
	Key, Summary, Status string
	Done                 bool      // its status is in the done category
	Start, End           time.Time // zero when unknown
	// DatesFromSprints marks Start/End taken from the children's sprints.
	DatesFromSprints       bool
	Children, DoneChildren int
	Points, DonePoints     float64
	Kids                   []EpicChild // in rank order
	// BlockedBy are the issues blocking it (its "is blocked by" links).
	BlockedBy []string
	// Parent is the plan-level issue above it (an initiative), "" for none.
	Parent, ParentSummary string
}

// EpicChild is an issue under an epic, dated by its own fields or its
// sprints.
type EpicChild struct {
	Key, Summary, Status, Type string
	TypeKind                   string // see TypeKind
	Done                       bool
	Start, End                 time.Time
	DatesFromSprints           bool
}

// roadmapFieldIDs are the instance's custom fields a roadmap reads.
type roadmapFieldIDs struct {
	start, end, sprint string
	dev                string            // the Development summary (PRs, branches)
	flagged            string            // the Flagged (impediment) field
	byName             map[string]string // every field's id by lower-cased name
}

// roadmapFieldsOf picks the fields out of the field metadata: "Start date"
// before Plans' "Target start", Plans' "Target end" (the due date is a
// system field), and the Agile sprint field.
func roadmapFieldsOf(fields []apiField) roadmapFieldIDs {
	ids := roadmapFieldIDs{byName: map[string]string{}}
	for _, f := range fields {
		// The English name decides what a field is; a translated one
		// (Jira's name in the user's language) is looked up too.
		name := strings.ToLower(f.Name)
		if _, ok := ids.byName[name]; !ok {
			ids.byName[name] = f.ID
		}
		if f.UntranslatedName != "" {
			name = strings.ToLower(f.UntranslatedName)
			if _, ok := ids.byName[name]; !ok {
				ids.byName[name] = f.ID
			}
		}
		switch {
		case name == "start date":
			ids.start = f.ID
		case name == "target start" || strings.HasSuffix(f.Schema.Custom, ":jpo-custom-field-baseline-start"):
			if ids.start == "" {
				ids.start = f.ID
			}
		case name == "target end" || strings.HasSuffix(f.Schema.Custom, ":jpo-custom-field-baseline-end"):
			ids.end = f.ID
		case f.Schema.Custom == "com.pyxis.greenhopper.jira:gh-sprint":
			ids.sprint = f.ID
		case strings.HasSuffix(f.Schema.Custom, ":devsummarycf"):
			ids.dev = f.ID
		case name == "flagged":
			ids.flagged = f.ID
		}
	}
	return ids
}

// apiField is one entry of /rest/api/3/field. UntranslatedName is the
// English name when the site shows field names in the user's language.
type apiField struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	UntranslatedName string `json:"untranslatedName"`
	Schema           struct {
		Type   string `json:"type"`
		Items  string `json:"items"`
		Custom string `json:"custom"`
		System string `json:"system"`
	} `json:"schema"`
}

// fieldMeta is the site's field metadata, read once a session: on a large
// site it runs to megabytes, and story points and the roadmap both want it.
func (c *Client) fieldMeta(ctx context.Context) ([]apiField, error) {
	c.mu.Lock()
	f := c.fields
	c.mu.Unlock()
	if f != nil {
		return f, nil
	}
	v, err, _ := c.shared.Do("field", func() (any, error) {
		var fields []apiField
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/field", "field metadata", nil, &fields); err != nil {
			return nil, err
		}
		if fields == nil {
			fields = []apiField{} // read, though empty
		}
		c.mu.Lock()
		c.fields = fields
		c.mu.Unlock()
		return fields, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]apiField), nil
}

func (c *Client) resolveRoadmapFields(ctx context.Context) (roadmapFieldIDs, error) {
	c.mu.Lock()
	if c.roadmapFields != nil {
		ids := *c.roadmapFields
		c.mu.Unlock()
		return ids, nil
	}
	c.mu.Unlock()
	fields, err := c.fieldMeta(ctx)
	if err != nil {
		return roadmapFieldIDs{}, err
	}
	ids := roadmapFieldsOf(fields)
	c.mu.Lock()
	c.roadmapFields = &ids
	c.mu.Unlock()
	return ids, nil
}

// Roadmap lists project's epics (issues of epicType) in rank order, open
// ones and those done in the last doneDays, with dates and their children's
// progress.
func (c *Client) Roadmap(ctx context.Context, project, epicType string, doneDays int) ([]Epic, error) {
	if !c.Enabled() {
		return nil, errNotConfigured
	}
	ids, err := c.resolveRoadmapFields(ctx)
	if err != nil {
		return nil, err
	}
	fields := []string{"summary", "status", "duedate", "issuelinks", "parent"}
	for _, id := range []string{ids.start, ids.end} {
		if id != "" {
			fields = append(fields, id)
		}
	}
	jql := fmt.Sprintf(`project = %q AND issuetype = %q AND (statusCategory != Done OR resolved >= -%dd) ORDER BY rank`, project, epicType, doneDays)
	raw, err := c.searchChart(ctx, jql, fields, "")
	if err != nil {
		return nil, err
	}
	epics := make([]Epic, len(raw))
	index := map[string]int{}
	for i, is := range raw {
		e := Epic{Key: is.Key}
		_ = json.Unmarshal(is.Fields["summary"], &e.Summary)
		e.Status, e.Done = statusOf(is.Fields["status"])
		var parent struct {
			Key    string `json:"key"`
			Fields struct {
				Summary string `json:"summary"`
			} `json:"fields"`
		}
		_ = json.Unmarshal(is.Fields["parent"], &parent)
		e.Parent, e.ParentSummary = parent.Key, parent.Fields.Summary
		var links []apiIssueLink
		_ = json.Unmarshal(is.Fields["issuelinks"], &links)
		for _, l := range links {
			if l.InwardIssue != nil && strings.EqualFold(l.Type.Inward, "is blocked by") {
				e.BlockedBy = append(e.BlockedBy, l.InwardIssue.Key)
			}
		}
		e.Start = dateField(is.Fields[ids.start])
		if e.End = dateField(is.Fields["duedate"]); e.End.IsZero() {
			e.End = dateField(is.Fields[ids.end])
		}
		epics[i] = e
		index[is.Key] = i
	}
	if err := c.addChildren(ctx, epics, index, ids.sprint); err != nil {
		return nil, err
	}
	return epics, nil
}

// addChildren counts each epic's children and points, and fills missing
// dates from the sprints they sit in.
func (c *Client) addChildren(ctx context.Context, epics []Epic, index map[string]int, sprintField string) error {
	ids, _ := c.resolveRoadmapFields(ctx)
	sp := c.resolveStoryPointFields(ctx)
	fields := append([]string{"summary", "status", "parent", "issuetype", "duedate"}, sp...)
	for _, id := range []string{sprintField, ids.start, ids.end} {
		if id != "" {
			fields = append(fields, id)
		}
	}
	type span struct{ start, end time.Time }
	spans := make([]span, len(epics))
	keys := make([]string, len(epics))
	for i, e := range epics {
		keys[i] = e.Key
	}
	for chunk := range slices.Chunk(keys, 100) {
		raw, err := c.searchAll(ctx, "parent in ("+strings.Join(chunk, ",")+") ORDER BY rank", fields)
		if err != nil {
			return err
		}
		for _, is := range raw {
			var p struct {
				Key string `json:"key"`
			}
			_ = json.Unmarshal(is.Fields["parent"], &p)
			i, ok := index[p.Key]
			if !ok {
				continue
			}
			e := &epics[i]
			kid := EpicChild{Key: is.Key}
			_ = json.Unmarshal(is.Fields["summary"], &kid.Summary)
			kid.Status, kid.Done = statusOf(is.Fields["status"])
			var typ named
			_ = json.Unmarshal(is.Fields["issuetype"], &typ)
			kid.Type, kid.TypeKind = typ.Name, TypeKind(typ.IconURL)
			kid.Start = dateField(is.Fields[ids.start])
			if kid.End = dateField(is.Fields["duedate"]); kid.End.IsZero() {
				kid.End = dateField(is.Fields[ids.end])
			}
			done := kid.Done
			pts := 0.0
			for _, id := range sp {
				if v, err := strconv.ParseFloat(string(is.Fields[id]), 64); err == nil {
					pts = v
					break
				}
			}
			e.Children++
			e.Points += pts
			if done {
				e.DoneChildren++
				e.DonePoints += pts
			}
			var sprints []struct {
				StartDate time.Time `json:"startDate"`
				EndDate   time.Time `json:"endDate"`
			}
			_ = json.Unmarshal(is.Fields[sprintField], &sprints)
			var own span
			for _, s := range sprints {
				if !s.StartDate.IsZero() && (own.start.IsZero() || s.StartDate.Before(own.start)) {
					own.start = s.StartDate
				}
				if s.EndDate.After(own.end) {
					own.end = s.EndDate
				}
				if !s.StartDate.IsZero() && (spans[i].start.IsZero() || s.StartDate.Before(spans[i].start)) {
					spans[i].start = s.StartDate
				}
				if s.EndDate.After(spans[i].end) {
					spans[i].end = s.EndDate
				}
			}
			if kid.Start.IsZero() && kid.End.IsZero() && !own.start.IsZero() {
				kid.Start, kid.End, kid.DatesFromSprints = day(own.start), day(own.end), true
			}
			e.Kids = append(e.Kids, kid)
		}
	}
	for i := range epics {
		e := &epics[i]
		if e.Start.IsZero() && !spans[i].start.IsZero() {
			e.Start, e.DatesFromSprints = day(spans[i].start), true
		}
		if e.End.IsZero() && !spans[i].end.IsZero() {
			e.End, e.DatesFromSprints = day(spans[i].end), true
		}
	}
	return nil
}

// statusOf is a status field's name and whether it is in the done category.
func statusOf(raw json.RawMessage) (string, bool) {
	var s struct {
		Name     string `json:"name"`
		Category struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	}
	_ = json.Unmarshal(raw, &s)
	return s.Name, s.Category.Key == "done"
}

// dateField reads a "2006-01-02" field, zero when empty.
func dateField(raw json.RawMessage) time.Time {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return time.Time{}
	}
	d, _ := time.ParseInLocation(time.DateOnly, s, time.Local)
	return d
}

// day is t's calendar day in local time.
func day(t time.Time) time.Time {
	t = t.Local()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// SetDates writes an issue's start (when the instance has a start field)
// and due date; a zero time clears it.
func (c *Client) SetDates(ctx context.Context, key string, start, end time.Time) error {
	if !c.Enabled() {
		return errNotConfigured
	}
	ids, err := c.resolveRoadmapFields(ctx)
	if err != nil {
		return err
	}
	val := func(t time.Time) any {
		if t.IsZero() {
			return nil
		}
		return t.Format(time.DateOnly)
	}
	fields := map[string]any{"duedate": val(end)}
	if ids.start != "" {
		fields[ids.start] = val(start)
	}
	if err := c.do(ctx, http.MethodPut, "/rest/api/3/issue/"+url.PathEscape(key), key, map[string]any{"fields": fields}, nil); err != nil {
		return err
	}
	c.Invalidate(key)
	return nil
}

// CanSetStart reports whether the instance has a start date field to write.
func (c *Client) CanSetStart(ctx context.Context) bool {
	ids, err := c.resolveRoadmapFields(ctx)
	return err == nil && ids.start != ""
}
