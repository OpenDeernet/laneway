package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2/styles"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/home"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/standup"
)

// options are the config's ui: section with defaults filled in.
type options struct {
	autoRefresh     time.Duration // 0: off
	staleAfter      time.Duration
	images          bool
	delight         bool     // small celebrations (ui.delight)
	threaded        bool     // replies in Jira's thread (ui.threaded_replies)
	newestFirst     bool     // ui.comment_order: newest
	flatComments    bool     // ui.comment_layout: flat
	skinTone        string   // "_medium_skin_tone" and the like (ui.skin_tone), "" for none
	updateCheck     bool     // a daily look for a newer release (ui.update_check)
	llm             []string // the ask command (ui.llm), nil for the default
	imageMaxRows    int
	panelPct        int
	panelDefault    int  // ui.panel_width, which a drag near it snaps back to
	cardLimit       int  // 0: the client's default
	lanes           bool // default mode
	dateFormat      string
	fields          cardFields
	listCols        []string            // ui.list_columns, every one of listColumnIDs
	layout          *cardLayout         // ui.card_layout, nil: cardFields in the usual places
	cardStyles      []cardStyle         // ui.card_styles
	quick           []jira.QuickFilter  // config presets, ids -1, -2, …
	views           []jiraView          // config JQL views
	laneLayouts     []config.LaneLayout // ui.lane_layouts, the usable ones
	boardQuick      bool                // the board's own quick filters (ui.board_quick_filters)
	savedFilters    bool                // starred Jira filters as views
	rememberFilters bool                // the assignee and quick filters outlive the session (ui.remember_filters)
	capacity        map[string]float64  // sprint points per person, "default" for the rest
	timerOnStart    bool                // S also starts the timer
	startAssigns    bool                // S also assigns the issue to you
	startStatus     string              // and moves it there, "" for none
	workdayStart    time.Duration       // from midnight: when a log on another day starts
	templates       map[string]string   // new issue descriptions by type, lower-cased
	velocitySprints int                 // closed sprints in the velocity chart
	staleDays       int                 // in progress longer than this shows red
	standup         standup.Settings    // the ui.standup_* options
	branchTemplate  string              // copy_branch's name
	workBranch      string              // start work's new branch
	workAgent       string              // the herdr agent kind start work launches
	agentView       string              // where an attached agent shows: fullscreen or panel
	workArgs        []string            // its arguments before the start prompt
	workCreate      []string            // the command that makes a missing worktree, nil: herdr's
	kanbanDoneDays  int                 // done work older than this leaves kanban boards
	epicType        string              // the roadmap's issue type
	myWorkJQL       string              // O's query
	home            []string            // the start screen's widgets, none: no start screen
	workdays        []time.Weekday      // nil: Monday to Friday
	inboxEvery      time.Duration       // 0: the inbox never syncs by itself
	inboxLookback   time.Duration       // how far back the inbox reaches
	inboxIssues     int                 // 0: the client's default
	timerRound      time.Duration       // 0: to the minute
	fullRefresh     time.Duration       // idle refreshes fetch changes only for this long
	clipboardImage  []string            // command printing the clipboard's PNG, nil: probe
	openCmd         []string            // command opening URLs and files, nil: the OS's
	filters         []config.NamedQuery // named / queries for the palette
	cardColors      string              // "ribbon" or "off"
	mouse           bool                // clicks, drags and the wheel
	doubleClick     time.Duration       // a second click within this is a double-click
	plainIcons      bool                // issue types as letters (ui.icons: plain)
	hideEmpty       bool                // empty extra fields fold (ui.empty_fields: hide)
	hideLanes       bool                // empty board columns hide (ui.empty_lanes: hide)
	roadmapDoneDays int                 // resolved epics older than this leave the roadmap
	codeTheme       string              // chroma style for code blocks
}

// cardFields is what a card or list row shows besides key and summary.
type cardFields struct {
	typ, priority, status, points, assignee, parent, pr, deploy, subtasks, due, flagged, age, avatar bool
	stale                                                                                            int // ui.stale_days, for age
}

var allCardFields = cardFields{true, true, true, true, true, true, true, true, true, true, true, true, true, 5}

func defaultOptions() options {
	return options{autoRefresh: 2 * time.Minute, staleAfter: time.Minute, images: true, imageMaxRows: 16, panelPct: 50, panelDefault: 50,
		lanes: true, dateFormat: "2006-01-02 15:04", fields: allCardFields, boardQuick: true, savedFilters: true, rememberFilters: true, delight: true, threaded: true, updateCheck: true, velocitySprints: 8, staleDays: 5, branchTemplate: defaultBranchTemplate, workBranch: defaultWorkBranch, workAgent: "claude", agentView: "fullscreen", kanbanDoneDays: defaultKanbanDoneDays, epicType: "Epic", myWorkJQL: myWorkJQL, inboxEvery: 5 * time.Minute, fullRefresh: 10 * time.Minute, inboxLookback: 7 * 24 * time.Hour, roadmapDoneDays: 90, standup: standup.Defaults, codeTheme: fallbackCodeTheme, cardColors: "ribbon", mouse: true, workdayStart: 9 * time.Hour, doubleClick: 400 * time.Millisecond}
}

// weekdays reads a day by its first three letters.
var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// presetCodeTheme is the chroma style matching each theme preset.
var presetCodeTheme = map[string]string{
	"tokyonight": "tokyonight-night", "catppuccin": "catppuccin-mocha", "gruvbox": "gruvbox",
	"dracula": "dracula", "nord": "nord", "onedark": "onedark",
	"rosepine": "rose-pine", "kanagawa": "kanagawa-wave", "monokai": "monokai",
	"catppuccin-latte": "catppuccin-latte", "catppuccin-frappe": "catppuccin-frappe", "catppuccin-macchiato": "catppuccin-macchiato", "rosepine-moon": "rose-pine-moon", "rosepine-dawn": "rose-pine-dawn", "tokyonight-storm": "tokyonight-storm", "tokyonight-day": "tokyonight-day", "gruvbox-light": "gruvbox-light", "solarized-dark": "solarized-dark", "solarized-light": "solarized-light", "kanagawa-dragon": "kanagawa-dragon", "kanagawa-lotus": "kanagawa-lotus", "onelight": "github", "emico": "github-dark",
}

// optionsFrom resolves c over the defaults. A bad value is reported and
// the default kept, so a typo never stops the app.
func optionsFrom(c config.UIConfig) (options, []string) {
	o := defaultOptions()
	var warn []string
	dur := func(name, v string, dst *time.Duration, allowOff bool) {
		switch v = strings.TrimSpace(v); {
		case v == "":
		case allowOff && (v == "off" || v == "0"):
			*dst = 0
		default:
			d, err := time.ParseDuration(v)
			if err != nil || d < 5*time.Second {
				warn = append(warn, i18n.Tf("ui.%s: %q is not a duration of 5s or more", name, v))
				return
			}
			*dst = d
		}
	}
	dur("auto_refresh", c.AutoRefresh, &o.autoRefresh, true)
	dur("stale_after", c.StaleAfter, &o.staleAfter, false)
	dur("inbox_every", c.InboxEvery, &o.inboxEvery, true)
	dur("full_refresh", c.FullRefresh, &o.fullRefresh, false)
	switch strings.ToLower(strings.TrimSpace(c.EmptyFields)) {
	case "", "show":
	case "hide":
		o.hideEmpty = true
	default:
		warn = append(warn, i18n.Tf("ui.empty_fields: %q is not show or hide", c.EmptyFields))
	}
	switch strings.ToLower(strings.TrimSpace(c.EmptyLanes)) {
	case "", "show":
	case "hide":
		o.hideLanes = true
	default:
		warn = append(warn, i18n.Tf("ui.empty_lanes: %q is not show or hide", c.EmptyLanes))
	}
	switch strings.ToLower(strings.TrimSpace(c.Icons)) {
	case "", "nerd":
	case "plain":
		o.plainIcons = true
	default:
		warn = append(warn, i18n.Tf("ui.icons: %q is not nerd or plain", c.Icons))
	}
	if v := strings.TrimSpace(c.DoubleClick); v != "" {
		if d, err := time.ParseDuration(v); err != nil || d < 100*time.Millisecond || d > 2*time.Second {
			warn = append(warn, i18n.Tf("ui.double_click: %q is not a duration from 100ms to 2s", v))
		} else {
			o.doubleClick = d
		}
	}
	switch v := strings.ToLower(strings.TrimSpace(c.AgentView)); v {
	case "":
	case "fullscreen", "panel":
		o.agentView = v
	default:
		warn = append(warn, i18n.Tf("ui.agent_view: %q is not fullscreen or panel", c.AgentView))
	}
	switch v := strings.ToLower(strings.TrimSpace(c.CardColors)); v {
	case "":
	case "ribbon", "off":
		o.cardColors = v
	default:
		warn = append(warn, i18n.Tf("ui.card_colors: %q is not ribbon or off", c.CardColors))
	}
	for i, f := range c.Filters {
		if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.Query) == "" {
			warn = append(warn, i18n.Tf("ui.filters[%d]: needs name and query", i))
			continue
		}
		o.filters = append(o.filters, f)
	}
	if f := strings.Fields(c.ClipboardImage); len(f) > 0 {
		o.clipboardImage = f
	}
	if f := strings.Fields(c.LLM); len(f) > 0 {
		o.llm = f
	}
	if f := strings.Fields(c.Open); len(f) > 0 {
		o.openCmd = f
	}
	switch v := strings.TrimSpace(c.TimerRound); {
	case v == "" || v == "off":
	default:
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute || d > 8*time.Hour {
			warn = append(warn, i18n.Tf("ui.timer_round: %q is not a duration of 1m–8h", v))
			break
		}
		o.timerRound = d
	}
	dur("inbox_lookback", c.InboxLookback, &o.inboxLookback, false)
	switch n := c.InboxIssues; {
	case n == 0:
	case n < 1 || n > 200:
		warn = append(warn, i18n.Tf("ui.inbox_issues: %d is not 1–200", n))
	default:
		o.inboxIssues = n
	}
	for name, pts := range c.Capacity {
		if pts < 0 {
			warn = append(warn, i18n.Tf("ui.capacity.%s: %v is below 0", name, pts))
			continue
		}
		if o.capacity == nil {
			o.capacity = map[string]float64{}
		}
		o.capacity[name] = pts
	}
	switch n := c.StaleDays; {
	case n == 0:
	case n < 1:
		warn = append(warn, i18n.Tf("ui.stale_days: %d is below 1", n))
	default:
		o.staleDays = n
	}
	sw, swarn := standup.Parse(c)
	o.standup, warn = sw, append(warn, swarn...)
	switch n := c.KanbanDoneDays; {
	case n == 0:
	case n < 1 || n > 365:
		warn = append(warn, i18n.Tf("ui.kanban_done_days: %d is not 1–365", n))
	default:
		o.kanbanDoneDays = n
	}
	for _, d := range c.Workdays {
		name := strings.ToLower(strings.TrimSpace(d)) + "   "
		wd, ok := weekdays[name[:3]] // mon, Monday
		if !ok {
			warn = append(warn, i18n.Tf("ui.workdays: %q is not a weekday", d))
			continue
		}
		o.workdays = append(o.workdays, wd)
	}
	if q := strings.TrimSpace(c.MyWorkJQL); q != "" {
		o.myWorkJQL = q
	}
	switch k := strings.TrimSpace(c.MeetingKey); {
	case k != "" && !jira.ValidKey(k):
		warn = append(warn, i18n.Tf("ui.meeting_key: %q is no issue key", c.MeetingKey))
	case k == "" && strings.TrimSpace(c.Calendar) != "":
		warn = append(warn, i18n.T("ui.calendar: set ui.meeting_key, the issue meetings are logged on"))
	}
	o.home = home.Pick(c.Home)
	for _, w := range c.Home {
		if len(home.Pick([]string{w})) == 0 && !slices.Contains(home.Retired, w) {
			warn = append(warn, i18n.Tf("ui.home: %q is none of %s", w, strings.Join(home.Widgets, ", ")))
		}
	}
	if t := strings.TrimSpace(c.RoadmapEpicType); t != "" {
		o.epicType = t
	}
	switch n := c.RoadmapDoneDays; {
	case n == 0:
	case n < 1 || n > 3650:
		warn = append(warn, i18n.Tf("ui.roadmap_done_days: %d is not 1–3650", n))
	default:
		o.roadmapDoneDays = n
	}
	switch n := c.VelocitySprints; {
	case n == 0:
	case n < 1 || n > 50:
		warn = append(warn, i18n.Tf("ui.velocity_sprints: %d is not 1–50", n))
	default:
		o.velocitySprints = n
	}
	if b := c.ReportBackwards; b != "" && b != "live" && b != "first" {
		warn = append(warn, i18n.Tf("ui.report_backwards: %q is not live or first", b))
	}
	for typ, md := range c.Templates {
		if o.templates == nil {
			o.templates = map[string]string{}
		}
		o.templates[strings.ToLower(typ)] = md
	}
	switch name := strings.TrimSpace(c.CodeTheme); {
	case name == "":
		if t, ok := presetCodeTheme[c.Theme["preset"]]; ok {
			o.codeTheme = t
		}
	case styles.Registry[name] == nil:
		warn = append(warn, i18n.Tf("ui.code_theme: unknown style %q", name))
	default:
		o.codeTheme = name
	}
	if tmpl := strings.TrimSpace(c.BranchTemplate); tmpl != "" {
		if bad := badBranchPlaceholder(tmpl); bad != "" {
			warn = append(warn, i18n.Tf("ui.branch_template: unknown %s", bad))
		} else {
			o.branchTemplate, o.workBranch = tmpl, tmpl
		}
	}
	if a := strings.TrimSpace(c.WorkAgent); a != "" {
		o.workAgent = a
	}
	o.workArgs = slices.DeleteFunc(slices.Clone(c.WorkArgs), func(a string) bool { return a == "" })
	if len(c.WorkCreate) > 0 && strings.TrimSpace(c.WorkCreate[0]) != "" {
		o.workCreate = slices.Clone(c.WorkCreate)
	}
	if tmpl := strings.TrimSpace(c.WorkBranchTemplate); tmpl != "" {
		if bad := badBranchPlaceholder(tmpl); bad != "" {
			warn = append(warn, i18n.Tf("ui.work_branch_template: unknown %s", bad))
		} else {
			o.workBranch = tmpl
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.StartAssigns)) {
	case "", "off":
	case "on":
		o.startAssigns = true
	default:
		warn = append(warn, i18n.Tf("ui.start_assigns: %q is not on or off", c.StartAssigns))
	}
	o.startStatus = strings.TrimSpace(c.StartStatus)
	if v := strings.TrimSpace(c.WorkdayStart); v != "" {
		if t, err := time.Parse("15:04", v); err != nil {
			warn = append(warn, i18n.Tf("ui.workday_start: %q is not a time (09:00)", v))
		} else {
			o.workdayStart = time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.TimerOnStart)) {
	case "", "off":
	case "on":
		o.timerOnStart = true
	default:
		warn = append(warn, i18n.Tf("ui.timer_on_start: %q is not on or off", c.TimerOnStart))
	}
	switch strings.ToLower(strings.TrimSpace(c.Mouse)) {
	case "", "on":
	case "off":
		o.mouse = false
	default:
		warn = append(warn, i18n.Tf("ui.mouse: %q is not on or off", c.Mouse))
	}
	switch strings.ToLower(strings.TrimSpace(c.BoardQuickFilters)) {
	case "", "on":
	case "off":
		o.boardQuick = false
	default:
		warn = append(warn, i18n.Tf("ui.board_quick_filters: %q is not on or off", c.BoardQuickFilters))
	}
	switch strings.ToLower(strings.TrimSpace(c.SavedFilters)) {
	case "", "on":
	case "off":
		o.savedFilters = false
	default:
		warn = append(warn, i18n.Tf("ui.saved_filters: %q is not on or off", c.SavedFilters))
	}
	switch strings.ToLower(strings.TrimSpace(c.RememberFilters)) {
	case "", "on":
	case "off":
		o.rememberFilters = false
	default:
		warn = append(warn, i18n.Tf("ui.remember_filters: %q is not on or off", c.RememberFilters))
	}
	switch strings.ToLower(strings.TrimSpace(c.UpdateCheck)) {
	case "", "on":
	case "off":
		o.updateCheck = false
	default:
		warn = append(warn, i18n.Tf("ui.update_check: %q is not on or off", c.UpdateCheck))
	}
	switch strings.ToLower(strings.TrimSpace(c.Delight)) {
	case "", "on":
	case "off":
		o.delight = false
	default:
		warn = append(warn, i18n.Tf("ui.delight: %q is not on or off", c.Delight))
	}
	switch strings.ToLower(strings.TrimSpace(c.ThreadedReplies)) {
	case "", "on":
	case "off":
		o.threaded = false
	default:
		warn = append(warn, i18n.Tf("ui.threaded_replies: %q is not on or off", c.ThreadedReplies))
	}
	switch strings.ToLower(strings.TrimSpace(c.CommentOrder)) {
	case "", "oldest":
	case "newest":
		o.newestFirst = true
	default:
		warn = append(warn, i18n.Tf("ui.comment_order: %q is not oldest or newest", c.CommentOrder))
	}
	switch strings.ToLower(strings.TrimSpace(c.CommentLayout)) {
	case "", "threaded":
	case "flat":
		o.flatComments = true
	default:
		warn = append(warn, i18n.Tf("ui.comment_layout: %q is not threaded or flat", c.CommentLayout))
	}
	switch t := strings.ToLower(strings.TrimSpace(c.SkinTone)); t {
	case "", "none":
	case "light", "medium_light", "medium", "medium_dark", "dark":
		o.skinTone = "_" + t + "_skin_tone"
	default:
		warn = append(warn, i18n.Tf("ui.skin_tone: %q is not light, medium_light, medium, medium_dark or dark", c.SkinTone))
	}
	switch strings.ToLower(strings.TrimSpace(c.Images)) {
	case "", "auto":
	case "off":
		o.images = false
	default:
		warn = append(warn, i18n.Tf("ui.images: %q is not auto or off", c.Images))
	}
	switch n := c.ImageMaxRows; {
	case n == 0:
	case n < 1 || n > 200:
		warn = append(warn, i18n.Tf("ui.image_max_rows: %d is not 1–200", n))
	default:
		o.imageMaxRows = n
	}
	switch n := c.PanelWidth; {
	case n == 0:
	case n < 20 || n > 80:
		warn = append(warn, i18n.Tf("ui.panel_width: %d is not 20–80", n))
	default:
		o.panelPct = n
	}
	switch n := c.CardLimit; {
	case n == 0:
	case n < 50 || n > 5000:
		warn = append(warn, i18n.Tf("ui.card_limit: %d is not 50–5000", n))
	default:
		o.cardLimit = n
	}
	switch strings.ToLower(strings.TrimSpace(c.DefaultMode)) {
	case "", "lanes":
	case "list":
		o.lanes = false
	default:
		warn = append(warn, i18n.Tf("ui.default_mode: %q is not lanes or list", c.DefaultMode))
	}
	if f := strings.TrimSpace(c.DateFormat); f != "" {
		o.dateFormat = f
	}
	for i, q := range c.QuickFilters {
		if strings.TrimSpace(q.Name) == "" || strings.TrimSpace(q.JQL) == "" {
			warn = append(warn, i18n.Tf("ui.quick_filters[%d]: needs name and jql", i))
			continue
		}
		o.quick = append(o.quick, jira.QuickFilter{ID: -1 - len(o.quick), Name: q.Name, JQL: q.JQL})
	}
	for i, v := range c.Views {
		if strings.TrimSpace(v.Name) == "" || strings.TrimSpace(v.JQL) == "" {
			warn = append(warn, i18n.Tf("ui.views[%d]: needs name and jql", i))
			continue
		}
		o.views = append(o.views, jiraView{kind: jiraViewJQL, name: v.Name, jql: v.JQL, lanes: true})
	}
	for i, l := range c.LaneLayouts {
		if strings.TrimSpace(l.Name) == "" || len(l.Lanes) == 0 {
			warn = append(warn, i18n.Tf("ui.lane_layouts[%d]: needs name and lanes", i))
			continue
		}
		o.laneLayouts = append(o.laneLayouts, l)
	}
	if c.CardFields != nil {
		var f cardFields
		for _, name := range c.CardFields {
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "type":
				f.typ = true
			case "priority":
				f.priority = true
			case "status":
				f.status = true
			case "points":
				f.points = true
			case "assignee":
				f.assignee = true
			case "parent":
				f.parent = true
			case "pr":
				f.pr = true
			case "deploy":
				f.deploy = true
			case "subtasks":
				f.subtasks = true
			case "due":
				f.due = true
			case "flagged":
				f.flagged = true
			case "age":
				f.age = true
			case "avatar":
				f.avatar = true
			default:
				warn = append(warn, i18n.Tf("ui.card_fields: unknown field %q", name))
			}
		}
		o.fields = f
	}
	o.fields.stale = o.staleDays
	if c.ListColumns != nil {
		var lcw []string
		o.listCols, lcw = listColumnsFrom(c.ListColumns)
		warn = append(warn, lcw...)
	}
	var lw, cw []string
	o.layout, lw = cardLayoutFrom(c.CardLayout, c.CustomFields)
	o.cardStyles, cw = cardStylesFrom(c.CardStyles, c.CustomFields)
	warn = append(append(warn, lw...), cw...)
	o.panelDefault = o.panelPct
	return o, warn
}
