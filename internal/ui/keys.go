package ui

import (
	"maps"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
)

// keyNames names every rebindable action for the ui.keys config.
func (k *keyMap) keyNames() map[string]*key.Binding {
	return map[string]*key.Binding{
		"up": &k.Up, "down": &k.Down, "left": &k.Left, "right": &k.Right,
		"top": &k.Home, "bottom": &k.End, "page_up": &k.PageUp, "page_down": &k.PageDown,
		"open": &k.OpenChannel, "toggle_panel": &k.OpenRef, "browser": &k.OpenAttach, "refresh": &k.Refresh,
		"status": &k.JiraStatus, "priority": &k.JiraPriority, "points": &k.JiraPoints, "summary": &k.JiraSummary, "labels": &k.JiraLabels,
		"assign": &k.JiraAssignee, "comment": &k.JiraComment, "reply": &k.JiraReply,
		"start_work": &k.JiraStart, "linked_issue": &k.JiraLinks, "back": &k.Back, "image": &k.Image,
		"quit": &k.Quit, "help": &k.Help, "search": &k.Search, "goto": &k.Goto, "create": &k.Create,
		"copy_key": &k.CopyKey, "copy_url": &k.CopyURL, "copy_branch": &k.CopyBranch,
		"move_left": &k.MoveCardLeft, "move_right": &k.MoveCardRight,
		"project": &k.Project, "board": &k.Board, "next_view": &k.NextView, "prev_view": &k.PrevView,
		"toggle_mode": &k.ToggleMode, "sort": &k.Sort, "move_sprint": &k.MoveSprint,
		"assignee_filter": &k.Assignee, "mine": &k.Mine, "clear_filters": &k.ClearFilters, "roadmap": &k.Roadmap, "palette": &k.Palette, "mark": &k.Mark, "mark_all": &k.MarkAll, "undo": &k.Undo, "bulk": &k.Bulk, "plan": &k.Plan, "charts": &k.Charts, "log_work": &k.LogWork, "description": &k.JiraDescription, "inbox": &k.Inbox, "issue_actions": &k.IssueActions, "site": &k.Site, "standup": &k.Standup, "history": &k.History, "development": &k.DevInfo, "pin": &k.Pin, "fold": &k.Fold, "unfold_all": &k.UnfoldAll, "next_band": &k.NextBand, "prev_band": &k.PrevBand, "jql": &k.JQL, "timer": &k.Timer, "timesheet": &k.Timesheet, "settings": &k.Settings, "filter_builder": &k.FilterBuilder, "panel_wider": &k.PanelWider, "panel_narrower": &k.PanelNarrower, "quick_edit": &k.QuickEdit, "my_work": &k.MyWork, "compact": &k.Compact, "empty_lanes": &k.EmptyLanes, "lane_layout": &k.LaneLayout, "arrange_lanes": &k.ArrangeLanes, "releases": &k.Releases, "repeat": &k.Repeat, "refine": &k.Refine, "notes": &k.Notes, "ask": &k.Ask, "time_machine": &k.TimeMachine, "closed_sprint": &k.ClosedSprint, "agent_back": &k.AgentBack, "agents": &k.Agents, "merge_requests": &k.MergeRequests, "home": &k.StartScreen,
		"plan_start": &k.PlanStart, "plan_goal": &k.PlanGoal, "plan_rename": &k.PlanRename, "plan_new": &k.PlanNew, "plan_complete": &k.PlanComplete,
		"rank_up": &k.RankUp, "rank_down": &k.RankDown, "rank_top": &k.RankTop, "rank_bottom": &k.RankBottom, "next_comment": &k.NextComment, "prev_comment": &k.PrevComment, "delete_comment": &k.DeleteComment, "standup_pause": &k.StandupPause, "standup_shuffle": &k.StandupShuffle, "standup_park": &k.StandupPark, "inbox_done": &k.InboxDone, "inbox_done_all": &k.InboxDoneAll, "inbox_unread": &k.InboxUnread, "inbox_snooze": &k.InboxSnooze, "agent_prompt": &k.AgentPrompt, "agent_stop": &k.AgentStop,
		"roadmap_grip": &k.RoadmapGrip, "roadmap_fold": &k.RoadmapFold, "zoom_in": &k.ZoomIn, "zoom_out": &k.ZoomOut, "today": &k.Today,
		"end_earlier": &k.EndEarlier, "end_later": &k.EndLater, "roadmap_issues": &k.RoadmapIssues, "roadmap_edit": &k.RoadmapEdit,
		"chart_done": &k.ChartDone, "chart_compare": &k.ChartCompare,
		"edit_entry": &k.EditEntry, "delete_entry": &k.DeleteEntry, "propose_work": &k.ProposeWork, "move_entry": &k.MoveEntry,
	}
}

// retiredKeys are actions laneway dropped; old configs naming them stay quiet.
var retiredKeys = []string{"review"}

// applyKeys rebinds the named actions, keeping each one's help text; "none"
// unbinds one. Unknown names and empty lists are reported and skipped.
func (k *keyMap) applyKeys(over map[string]config.KeyList) []string {
	names := k.keyNames()
	var warn []string
	for _, name := range slices.Sorted(maps.Keys(over)) {
		keys := over[name]
		b, ok := names[name]
		switch {
		case slices.Contains(retiredKeys, name):
			continue
		case !ok:
			warn = append(warn, i18n.Tf("ui.keys: unknown action %q", name))
			continue
		case len(keys) == 0 || slices.Contains(keys, ""):
			warn = append(warn, i18n.Tf("ui.keys.%s: no key", name))
			continue
		case slices.Equal(keys, []string{config.NoKey}):
			*b = key.NewBinding(key.WithHelp("", b.Help().Desc), key.WithDisabled())
			continue
		}
		*b = key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys[0], b.Help().Desc))
	}
	return append(warn, k.clashes()...)
}

// keyScopes lists the actions live at once, by the ui.keys name. Within
// one scope a key must do one thing.
var keyScopes = []struct {
	name    string
	actions []string
}{
	{"board", []string{
		"up", "down", "left", "right", "top", "bottom", "page_up", "page_down",
		"open", "toggle_panel", "browser", "refresh", "quit", "help", "search", "goto", "create",
		"copy_key", "copy_url", "copy_branch", "move_left", "move_right", "project", "board",
		"next_view", "prev_view", "toggle_mode", "sort", "move_sprint", "assignee_filter", "mine", "clear_filters", "roadmap", "palette", "mark", "mark_all", "undo", "bulk", "plan", "charts", "timer", "timesheet", "inbox", "site", "standup", "jql", "pin", "fold", "unfold_all", "next_band", "prev_band", "settings", "filter_builder", "panel_wider", "panel_narrower", "quick_edit", "my_work", "compact", "empty_lanes", "releases", "repeat", "refine", "rank_up", "rank_down", "rank_top", "rank_bottom", "closed_sprint", "agents", "merge_requests", "home",
	}},
	{"panel", []string{
		"status", "priority", "points", "summary", "labels", "assign", "comment", "reply", "start_work", "search",
		"linked_issue", "back", "image", "browser", "copy_key", "copy_url", "copy_branch", "help", "refresh", "toggle_panel", "palette",
		"log_work", "timer", "timesheet", "description", "inbox", "issue_actions", "standup", "history", "development", "pin", "next_view", "prev_view", "settings", "panel_wider", "panel_narrower", "undo", "next_comment", "prev_comment", "delete_comment", "agents",
	}},
	{"planning", []string{
		"up", "down", "left", "right", "top", "bottom", "page_up", "page_down", "quit", "plan", "next_view", "prev_view",
		"mark", "move_sprint", "plan_start", "plan_goal", "plan_rename", "plan_new", "plan_complete", "rank_up", "rank_down",
		"search", "quick_edit", "bulk", "undo",
		"refresh", "browser", "open", "toggle_panel", "help", "copy_key",
	}},
	{"roadmap", []string{
		"up", "down", "left", "right", "top", "bottom", "quit", "roadmap", "roadmap_grip", "roadmap_fold",
		"move_left", "move_right", "end_earlier", "end_later", "zoom_in", "zoom_out", "today", "refresh", "roadmap_issues",
		"create", "browser", "open", "toggle_panel", "copy_key", "help", "search", "undo", "roadmap_edit",
	}},
	{"charts", []string{"prev_view", "next_view", "refresh", "copy_key", "help", "quit", "charts", "chart_done", "chart_compare"}},
	{"timesheet", []string{"prev_view", "next_view", "copy_key", "edit_entry", "delete_entry", "propose_work", "mark", "mark_all", "move_entry", "timesheet"}},
	{"standup", []string{"up", "down", "left", "right", "open", "fold", "assignee_filter", "standup_pause", "standup_shuffle", "standup_park", "prev_view", "next_view", "copy_key", "refresh", "help", "quit", "standup"}},
	{"inbox", []string{"up", "down", "top", "bottom", "page_up", "page_down", "open", "browser", "comment", "reply", "inbox_done", "inbox_done_all", "inbox_unread", "inbox_snooze", "copy_key", "refresh", "help", "quit", "inbox"}},
	{"agents", []string{"up", "down", "top", "bottom", "open", "toggle_panel", "browser", "agent_prompt", "agent_stop", "copy_key", "refresh", "help", "quit", "agents"}},
	{"merge_requests", []string{"up", "down", "top", "bottom", "page_up", "page_down", "open", "browser", "refresh", "help", "quit", "merge_requests"}},
	{"week", []string{"up", "down", "left", "right", "prev_view", "next_view", "open", "goto", "copy_key", "refresh", "help", "quit", "timesheet"}},
}

// clashes reports keys bound to two actions in one scope.
func (k *keyMap) clashes() []string {
	names := k.keyNames()
	var warn []string
	for _, s := range keyScopes {
		owner := map[string]string{}
		for _, name := range s.actions {
			for _, kk := range names[name].Keys() {
				if prev, ok := owner[kk]; ok {
					warn = append(warn, i18n.Tf("ui.keys: %q is both %s and %s on the %s", kk, prev, name, s.name))
					continue
				}
				owner[kk] = name
			}
		}
	}
	return warn
}

// keysLabel is a binding's keys for the help overlay: "H / shift+left".
func keysLabel(b key.Binding) string {
	return strings.Join(b.Keys(), " / ")
}
