// Package ui is the Jira board TUI: matterbox's Jira tab and issue panel,
// standalone.
package ui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/editor"
	"github.com/cornedor/laneway/internal/forge/gitlab"
	"github.com/cornedor/laneway/internal/herdr"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/index"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/rules"
	"github.com/cornedor/laneway/internal/store"
	"github.com/cornedor/laneway/internal/viewport"
)

type focus int

const (
	focusJira focus = iota
	focusRef
)

type keyMap struct {
	Tab, ShiftTab                     key.Binding
	Up, Down, Left, Right             key.Binding
	Home, End                         key.Binding
	InputUp, InputDown                key.Binding
	PageUp, PageDown                  key.Binding
	OpenChannel, OpenRef              key.Binding
	OpenAttach, Refresh               key.Binding
	JiraStatus, JiraPriority          key.Binding
	JiraPoints, JiraAssignee          key.Binding
	JiraSummary, JiraLabels           key.Binding
	JiraComment, JiraReply, JiraStart key.Binding
	JiraLinks, Back, Image            key.Binding

	// The board's own keys.
	Quit, Help, Search, Goto, Create   key.Binding
	CopyKey, CopyURL, CopyBranch       key.Binding
	MoveCardLeft, MoveCardRight        key.Binding
	Project, Board, NextView, PrevView key.Binding
	ToggleMode, Sort, MoveSprint       key.Binding
	Assignee, Mine, ClearFilters       key.Binding
	Roadmap, Palette, Mark, Bulk, Plan key.Binding
	MarkAll, Undo                      key.Binding
	Charts, LogWork, Timer, Timesheet  key.Binding
	ChartDone, ChartCompare            key.Binding
	JiraDescription, Inbox             key.Binding
	IssueActions, Site, Standup        key.Binding
	History, DevInfo, JQL, Pin         key.Binding
	Fold, UnfoldAll, Settings          key.Binding
	NextBand, PrevBand                 key.Binding
	FilterBuilder, PanelWider          key.Binding
	PanelNarrower, QuickEdit, MyWork   key.Binding
	Compact, Releases, Repeat          key.Binding
	EmptyLanes, MergeRequests          key.Binding
	LaneLayout, ArrangeLanes           key.Binding
	Refine, Notes, Ask, TimeMachine    key.Binding
	ClosedSprint, StartScreen          key.Binding
	AgentBack                          key.Binding // out of the agent's terminal in the panel
	Agents, AgentPrompt, AgentStop     key.Binding // the agents screen (agents_screen.go)

	// Planning's, the roadmap's and the timesheet's own keys.
	PlanStart, PlanGoal, PlanRename     key.Binding
	PlanNew, PlanComplete               key.Binding
	RankUp, RankDown                    key.Binding
	RankTop, RankBottom                 key.Binding
	NextComment, PrevComment            key.Binding
	DeleteComment                       key.Binding
	StandupPause, StandupShuffle        key.Binding
	StandupPark                         key.Binding
	InboxDone, InboxDoneAll             key.Binding
	InboxUnread, InboxSnooze            key.Binding
	RoadmapGrip, RoadmapFold            key.Binding
	ZoomIn, ZoomOut, Today              key.Binding
	EndEarlier, EndLater, RoadmapIssues key.Binding
	RoadmapEdit                         key.Binding
	EditEntry, DeleteEntry, ProposeWork key.Binding
	MoveEntry                           key.Binding
}

func bind(help string, keys ...string) key.Binding {
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys[0], help))
}

func defaultKeys() keyMap {
	return keyMap{
		Tab:          bind(i18n.T("focus next"), "tab"),
		ShiftTab:     bind(i18n.T("focus prev"), "shift+tab"),
		Up:           bind(i18n.T("up"), "up", "k"),
		Down:         bind(i18n.T("down"), "down", "j"),
		Left:         bind(i18n.T("left"), "left", "h"),
		Right:        bind(i18n.T("right"), "right", "l"),
		Home:         bind(i18n.T("top"), "home", "g"),
		End:          bind(i18n.T("bottom"), "end", "G"),
		InputUp:      bind(i18n.T("up"), "up", "ctrl+p"),
		InputDown:    bind(i18n.T("down"), "down", "ctrl+n"),
		PageUp:       bind(i18n.T("page up"), "pgup", "ctrl+u"),
		PageDown:     bind(i18n.T("page down"), "pgdown", "ctrl+d"),
		OpenChannel:  bind(i18n.T("open"), "enter"),
		OpenRef:      bind(i18n.T("open issue"), "v"),
		OpenAttach:   bind(i18n.T("open in browser"), "o"),
		Refresh:      bind(i18n.T("refresh"), "r"),
		JiraStatus:   bind(i18n.T("change status"), "s"),
		JiraPriority: bind(i18n.T("change priority"), "p"),
		JiraPoints:   bind(i18n.T("set story points"), "P"),
		JiraSummary:  bind(i18n.T("edit summary"), "e"),
		JiraLabels:   bind(i18n.T("edit labels"), "l"),
		JiraAssignee: bind(i18n.T("change assignee"), "a"),
		JiraComment:  bind(i18n.T("add comment"), "c"),
		JiraReply:    bind(i18n.T("reply to comment"), "R"),
		JiraStart:    bind(i18n.T("start work: agent, branch and prompt, then a herdr worktree"), "S"),
		JiraLinks:    bind(i18n.T("go to linked issue or web link (a Confluence page reads in the panel)"), "L"),
		Back:         bind(i18n.T("previous issue"), "backspace"),
		Image:        bind(i18n.T("view images"), "i"),

		Quit:            bind(i18n.T("quit"), "q"),
		Help:            bind(i18n.T("help"), "?"),
		Search:          bind(i18n.T("search"), "/"),
		Goto:            bind(i18n.T("go to issue by key"), "#"),
		Create:          bind(i18n.T("new issue"), "n"),
		CopyKey:         bind(i18n.T("copy key"), "y"),
		CopyURL:         bind(i18n.T("copy URL"), "Y"),
		CopyBranch:      bind(i18n.T("copy branch name"), "ctrl+y"),
		MoveCardLeft:    bind(i18n.T("move card left"), "H", "shift+left"),
		MoveCardRight:   bind(i18n.T("move card right"), "L", "shift+right"),
		Project:         bind(i18n.T("project"), "p"),
		Board:           bind(i18n.T("board"), "b"),
		NextView:        bind(i18n.T("next view"), "]"),
		PrevView:        bind(i18n.T("previous view"), "["),
		ToggleMode:      bind(i18n.T("lanes / list"), "t"),
		MoveSprint:      bind(i18n.T("move to sprint / backlog"), "M"),
		Sort:            bind(i18n.T("sort the list"), "s"),
		Assignee:        bind(i18n.T("assignee filter"), "a"),
		Mine:            bind(i18n.T("only mine"), "m"),
		ClearFilters:    bind(i18n.T("clear filters"), "0"),
		Roadmap:         bind(i18n.T("roadmap"), "R"),
		Palette:         bind(i18n.T("command palette"), ":"),
		Mark:            bind(i18n.T("mark card"), "x"),
		MarkAll:         bind(i18n.T("mark the lane / every row"), "X"),
		Undo:            bind(i18n.T("undo the last change"), "u"),
		Bulk:            bind(i18n.T("edit marked cards"), "B"),
		Plan:            bind(i18n.T("sprint planning"), "P"),
		Charts:          bind(i18n.T("sprint charts"), "C"),
		ChartDone:       bind(i18n.T("charts: the column done counts from"), "d"),
		ChartCompare:    bind(i18n.T("charts: a second line beside done"), "c"),
		LogWork:         bind(i18n.T("log work"), "w"),
		JiraDescription: bind(i18n.T("edit description"), "E"),
		Timer:           bind(i18n.T("start / stop the timer"), "T"),
		Timesheet:       bind(i18n.T("today's worklogs"), "W"),
		Inbox:           bind(i18n.T("inbox"), "I"),
		IssueActions:    bind(i18n.T("subtask, link, clone, watch"), "A"),
		Site:            bind(i18n.T("switch or add a Jira site"), "@"),
		Standup:         bind(i18n.T("standup: go round the team"), "U"),
		History:         bind(i18n.T("issue history"), "H"),
		DevInfo:         bind(i18n.T("pull requests and branches"), "D"),
		Pin:             bind(i18n.T("pin / unpin issue; on a panel field, pin it"), "*"),
		Fold:            bind(i18n.T("fold the swimlane, or the stacked lane's section"), "z"),
		UnfoldAll:       bind(i18n.T("unfold every swimlane and section"), "Z"),
		NextBand:        bind(i18n.T("next swimlane or group"), "}"),
		PrevBand:        bind(i18n.T("previous swimlane or group"), "{"),
		JQL:             bind(i18n.T("JQL search"), "Q"),
		Settings:        bind(i18n.T("settings"), ","),
		FilterBuilder:   bind(i18n.T("filter builder"), "F"),
		PanelWider:      bind(i18n.T("widen the panel"), "<"),
		PanelNarrower:   bind(i18n.T("narrow the panel"), ">"),
		QuickEdit:       bind(i18n.T("quick edit the card"), "e"),
		MyWork:          bind(i18n.T("my work, every project"), "O"),
		Compact:         bind(i18n.T("one-line cards"), "c"),
		EmptyLanes:      bind(i18n.T("hide / show empty lanes"), "alt+e"),
		LaneLayout:      bind(i18n.T("next lane layout (ui.lane_layouts)"), "alt+l"),
		ArrangeLanes:    bind(i18n.T("arrange the lanes: stack, split, move, rename, hide columns"), "alt+L", "alt+shift+l"),
		Releases:        bind(i18n.T("releases: versions and their progress"), "V"),
		Repeat:          bind(i18n.T("do the last change again on the selected card"), "."),
		Refine:          bind(i18n.T("refine: the view's issues one at a time"), "ctrl+e"),
		Notes:           bind(i18n.T("private notes on the issue ($EDITOR)"), "N"),
		Ask:             bind(i18n.T("ask ui.llm about the issue"), "ctrl+a"),
		TimeMachine:     bind(i18n.T("time machine: the board on earlier days"), "ctrl+t"),
		ClosedSprint:    bind(i18n.T("closed sprints: one as it ended"), "ctrl+o"),
		StartScreen:     bind(i18n.T("home: my work, inbox, sprint, timer, saved searches"), "~"),
		AgentBack:       bind(i18n.T("back from the agent's terminal to its issue"), "ctrl+\\"),
		Agents:          bind(i18n.T("agents: every herdr agent and worktree, by state"), "ctrl+g"),
		MergeRequests:   bind(i18n.T("merge requests waiting on you, every GitLab"), "alt+m"),
		AgentPrompt:     bind(i18n.T("agents: send the agent a prompt"), "p"),
		AgentStop:       bind(i18n.T("agents: stop the agent (twice)"), "d"),
		PlanStart:       bind(i18n.T("start the sprint / move its end"), "S"),
		PlanGoal:        bind(i18n.T("edit the sprint's goal"), "E"),
		PlanRename:      bind(i18n.T("rename the sprint"), "R"),
		PlanNew:         bind(i18n.T("new sprint"), "N"),
		PlanComplete:    bind(i18n.T("complete the sprint (twice)"), "C"),
		RankUp:          bind(i18n.T("rank up"), "K"),
		RankDown:        bind(i18n.T("rank down"), "J"),
		RankTop:         bind(i18n.T("rank to the top"), "alt+k"),
		RankBottom:      bind(i18n.T("rank to the bottom"), "alt+j"),
		NextComment:     bind(i18n.T("next comment"), "}"),
		PrevComment:     bind(i18n.T("previous comment"), "{"),
		DeleteComment:   bind(i18n.T("delete the comment (twice)"), "delete"),
		StandupPause:    bind(i18n.T("standup: start / pause the timer"), "space"),
		StandupShuffle:  bind(i18n.T("standup: a random order / the board's"), "s"),
		StandupPark:     bind(i18n.T("standup: park the card for after"), "P"),
		InboxDone:       bind(i18n.T("inbox: done, until something new"), "e"),
		InboxDoneAll:    bind(i18n.T("inbox: every read thread done"), "E"),
		InboxUnread:     bind(i18n.T("inbox: read / unread"), "u"),
		InboxSnooze:     bind(i18n.T("inbox: snooze till the next workday"), "s"),
		RoadmapGrip:     bind(i18n.T("grip the bar's start, end, let go"), "e"),
		RoadmapFold:     bind(i18n.T("fold the epic's issues"), "space"),
		ZoomIn:          bind(i18n.T("zoom in"), "+", "="),
		ZoomOut:         bind(i18n.T("zoom out"), "-"),
		Today:           bind(i18n.T("back to today"), "."),
		EndEarlier:      bind(i18n.T("end a column earlier"), "<"),
		EndLater:        bind(i18n.T("end a column later"), ">"),
		RoadmapIssues:   bind(i18n.T("the epic's issues as a view"), "f"),
		RoadmapEdit:     bind(i18n.T("quick edit the row's issue"), "E"),
		EditEntry:       bind(i18n.T("edit the entry"), "e"),
		DeleteEntry:     bind(i18n.T("delete the entry (twice)"), "d", "delete"),
		ProposeWork:     bind(i18n.T("propose worklogs from git and ui.activity"), "p"),
		MoveEntry:       bind(i18n.T("move the entry, or the marked ones, to another issue"), "m"),
	}
}

// hit is what a screen cell holds: on the board, idx is the lane (-1 in list
// mode) and line the card's row in it, -1 over no card.
type hitZone int

const (
	hitNone hitZone = iota
	hitJira
	hitRef
)

type hit struct {
	zone      hitZone
	idx, line int
	band      string
	section   int    // a stacked lane's section header hit: its index + 1, 0 for none
	sortBy    int    // a list column header hit: its jiraSort + 1, 0 for none
	headCol   string // a list column header hit: its column
}

// Model is the whole app. Held by value like matterbox's; the board state
// sits behind a pointer.
type Model struct {
	ctx   context.Context
	store *store.Store
	// pins are the pinned issue keys (palette.go).
	pins   map[string]bool
	keys   keyMap
	width  int
	height int
	focus  focus
	status string

	// rules fire on board changes, logged to rulesLog.
	rules    *rules.Set
	rulesLog string

	jiraClient *jira.Client
	// eventRefreshPending: a change-stream refresh is armed (events.go).
	eventRefreshPending bool
	eventInboxPending   bool              // a change-stream inbox sync is armed
	gitlab              *gitlab.Sites     // nil: none
	gitlabRepos         map[string]string // checkouts by GitLab project path
	mrReviewing         map[string]bool   // agent reviews starting, by group/project!iid
	index               *index.Index      // nil: none
	jiraProjects        []string
	jiraRepos           map[string]string
	jiraStartPrompt     string
	jiraStartStatus     map[string]string // by project, over ui.start_status
	jiraStarting        map[string]bool
	herdr               *herdr.Client
	demo                bool // laneway -demo (WithDemo)
	emojiImg            *emojiImages

	jiraTab  *jiraTabState
	jiraForm *jiraFormState

	// The issue panel.
	refOpen    bool
	refs       []reference
	refIdx     int
	refBack    []refCrumb // issues the panel came from by links, newest last (ref.go)
	refGen     int
	refLoading bool
	refErr     error
	refView    viewport.Model
	jiraIssue  *jira.Issue
	panelHint  string

	helpOpen bool
	helpPage int // the help's page when it is wider than the screen
	// quitAsked is set once a quit was held back for unsent work; the next
	// quit goes. Any other key clears it.
	quitAsked bool
	// statusLog keeps the status line's messages (messages.go);
	// statusLogged is the last one kept.
	statusLog     []statusEntry
	statusLogged  string
	statusAt      time.Time  // when the status line last changed
	statusErr     string     // the status line's text when it is an error (fail)
	lastDownload  string     // the attachment saved last, for the palette to open
	warnings      []string   // the startup warnings
	homeShown     bool       // ui.home's start screen opened once
	page          *panelPage // a Confluence page the panel shows over its issue (page.go)
	pageGen       int
	mr            *panelMR     // a GitLab merge request the panel shows over its issue (mrview.go)
	diff          *diffState   // the merge request's diff over the body (diffview.go)
	jobLog        *jobLogState // a CI job's log over the body (joblog.go)
	jobLogGen     int
	mrGen         int
	settings      *settingsView   // the , overlay (settings.go)
	filterBuilder *filterBuilder  // the F overlay (filter_builder.go)
	descEdit      *descEdit       // the in-app editor on a description, field or comment (description.go)
	quickKey      string          // the card a quick edit (e) changes, "" for the marks (bulk.go)
	uiConfig      config.UIConfig // as the file gives it, for settings
	configPath    string
	images        *panelImages
	opts          options

	jiraGotoActive bool
	jiraGotoInput  textinput.Model
	jiraGotoErr    string // why the key typed can't be opened, under the input
	jiraGotoKey    string // the key being looked up, "" when none

	// imageView shows the panel's images full size (image_view.go).
	imageView    bool
	imageViewIdx int

	jiraCreateActive                    bool
	jiraCreateType                      string
	lastCreateType                      map[string]string             // per project, the type n last created
	createFieldCache                    map[string][]jira.CreateField // project/type's create screen
	createMore                          bool                          // the create form shows the fields not required
	jiraCreateParent, jiraCreateProject string
	jiraCreateClone                     string // the issue the create form clones
	// jiraCreateReload reloads the roadmap once the new issue (an epic
	// made from it) exists.
	jiraCreateReload bool
	// jiraLinkChoice is the link type and direction picked for "link",
	// linkFind the search for the issue to link (link_search.go).
	jiraLinkChoice jiraPickerItem
	linkFind       linkFind

	jiraPicker jiraPickerState
	// fieldCursor is the panel's selected field (panel_fields.go), -1 for
	// none; it holds only while fieldCursorKey is the shown issue.
	fieldCursor    int
	fieldCursorKey string
	// paletteFocus is the pane the palette was opened from; its actions
	// run there.
	paletteFocus focus
	// timer runs on an issue (worklog.go); worklogStart is when the work
	// being logged began, zero for "back from now".
	timer        workTimer
	worklogStart time.Time
	// worklogEdit is the worklog the input edits (id, on worklogEditDay's
	// timesheet), "" for a new one; worklogComment its comment as the
	// input showed it, which left alone stays as Jira has it.
	worklogEdit    string
	worklogEditDay time.Time
	worklogComment string
	// worklogMoves are the worklogs (key/id) the move input moves, from
	// worklogEditDay's timesheet.
	worklogMoves []string
	// worklogFromTimer marks the input as the timer's stop: the timer ends
	// once the log is written.
	worklogFromTimer bool
	// timerNext is the card T was pressed on while the timer ran on
	// another: logging starts the timer there, ctrl+t moves it there.
	timerNext string
	// timerDropArmed is a first ctrl+d on a long timer: again drops it.
	timerDropArmed bool
	// sites are the configured Jira sites ("" is jira:), site the shown
	// one; nextSite is set when the app ends to switch, addSite when it
	// ends to add one (sites.go).
	sites    []string
	site     string
	nextSite *string
	addSite  bool
	// prefetchSeq debounces the cursor's prefetch (prefetch.go).
	prefetchSeq int
	// jql is the open JQL search input (jql.go).
	jql *jqlState
	// jiraCommentMentions are the people @-completed in the composer, and
	// jiraMention its open completion (mention.go).
	jiraCommentMentions []jira.Mention
	jiraMention         mentionState
	// inbox is every site's inbox threads and their marks; inboxUnread
	// the header's count of unread threads (inbox.go); mentionsSeen the
	// newest mention notified, started when the app began.
	inbox        *inboxData
	inboxUnread  int
	mentionsSeen time.Time
	// agents are the herdr agents by issue, worst state first (agents.go);
	// nil while herdr doesn't answer.
	agents map[string][]herdr.Agent
	// worktrees are the issues' linked worktrees in jira.repos, by key.
	worktrees map[string]string
	// agentPane is the agent the prompt being typed goes to.
	agentPane string
	// agentTerm is the agent attached in the panel (agent_panel.go), for
	// agentTermKey; agentTermDrag a drag the agent takes.
	agentTerm *termSession
	// agentTermScreen: agentTerm shows on the agents screen, not the panel.
	agentTermScreen bool
	agentTermKey    string
	agentTermDrag   bool
	started         time.Time
	// version is the running build's; upgradeCmd the command that updates
	// it, "" for the release page; newRelease a newer release, once seen
	// (update_check.go).
	version, upgradeCmd, newRelease string
	// panelExtra is panelExtraKey's other editable fields (editmeta);
	// panelEditID is the one being edited.
	panelExtra    []jiraFormField
	panelExtraKey string
	panelFacts    jira.Facts     // panelExtraKey's read-only details (created, watchers, time)
	branchKey     string         // the issue of the git branch laneway started in
	webLinks      []jira.WebLink // webLinksKey's remote links
	webLinksKey   string
	children      []jira.Child // webLinksKey's child issues, when it is an epic
	showEmpty     bool         // the empty fields ui.empty_fields: hide folds, shown for the session
	moreFields    bool         // the panel's More row open, for the session
	// panelExtraEarly: panelExtra is the remembered screen (earlyExtra), editmeta not in yet.
	panelExtraEarly bool
	fieldPins       map[string]bool // the panel's field pins (jira.PinsMeta)
	descOpen        map[string]bool // the description expands opened, by "KEY#n"; closed as in Jira
	descTasks       []bool          // the action items the panel's description shows: done or not
	// panelHits are the panel's clickable lines by content line: a field's
	// index or a linked issue's key (panel_mouse.go); panelFieldLine is each
	// field's line as the last render wrote it.
	panelHits      map[int]panelHit
	labels         labelSuggest // the labels input's suggestions
	panelFind      string       // what / last looked for in the panel
	panelFindAt    int          // the line of the last hit shown
	panelSel       panelSel     // text selected with the mouse (panel_select.go)
	panelPlain     []string     // the panel's lines, unstyled, for find
	panelSoft      []bool       // which of them a wrap continues on the next
	panelFieldLine []int
	// pickerLine is the content line of the inline picker's first shown row
	// (pickerStart), -1 when none (jira_edit.go).
	pickerLine, pickerStart int
	// inlineLine is the content line the inline editor starts on, -1 when
	// none; commentIndent the reply bars before it in the thread
	// (description.go).
	inlineLine, commentIndent int
	// formArea is where the form's description editor starts inside its
	// box, while it is drawn; nil otherwise (jira_transition.go).
	formArea *point
	// activityTab is the Activity section's open tab, activityLine its tab
	// row's content line (-1 when not drawn); activity the history and
	// worklogs it shows; commentHeads the comments' bylines (activity.go).
	activityTab  int
	activityLine int
	activity     activityState
	commentHeads []commentHead
	panelEditID  string

	// The one-line field input: story points, the summary or labels.
	jiraFieldActive bool
	jiraFieldName   string // "points", "summary", "labels" or "field" (panelEditID)
	jiraFieldKey    string
	jiraFieldInput  textinput.Model
	jiraFieldAsked  string // the bulk value a first enter asked to confirm

	jiraCommentActive  bool
	jiraCommentKey     string
	jiraCommentInput   editor.Model
	jiraCommentMention *jira.Mention
	jiraCommentReplyTo string
	jiraCommentReplyID string // the comment replied to, its composer drawn under it
	commentDelete      string // the comment whose delete a second click or key confirms
	// commentCursor is the comment } and { selected on commentCursorKey,
	// an index into its comments.
	commentCursor    int
	commentCursorKey string
	// jiraCommentBefore is the text it opened with (a reply's quote), so esc
	// asks once only for text you wrote; jiraCommentDiscard is that ask.
	jiraCommentBefore  string
	jiraCommentDiscard bool
	// unsent is a comment whose post failed, by issue: c brings it back.
	unsent unsentComment
	// jiraCommentMaybe is the comment c brought back when its post may
	// have landed: posting the same text looks for it first.
	jiraCommentMaybe unsentComment
	// otherSites are the other configured sites' clients, for the inbox
	// (inbox_sites.go).
	otherSites *siteClients
	// defaultSiteName is jira: name:, the default site's label.
	defaultSiteName string
	// actions are ui.actions, the usable ones (actions.go).
	actions []config.Action
	// queued counts the offline writes waiting (queue.go).
	queued int
	// refine is the refinement queue being stepped through (refine.go).
	refine *refineState
	// repeat is the last change, for . to make again (repeat.go).
	repeat *repeatAction
	// draftPending is a draft write scheduled (drafts.go).
	draftPending bool
	// jiraCommentVis is who the comment being written is for; commentVis
	// the choices by project, once asked (jira_comment.go).
	jiraCommentVis jira.Visibility
	commentVis     map[string][]jira.Visibility

	// panelResizing is set while the panel's left border is dragged
	// (panel_resize.go).
	panelResizing bool
	// panelResizeFrom is the width the drag started at, for esc.
	panelResizeFrom int
	// panelScrolling is set while the panel's scrollbar is dragged, from
	// the offset it started at, for esc.
	panelScrolling  bool
	panelScrollFrom int
	// lastClick detects a double-click.
	lastClick struct {
		at   time.Time
		x, y int
	}
	// mouse is where the pointer last moved (hover.go), mouseIn whether it
	// has; pointer the shape set, pointerOn whether the terminal gets one.
	mouseX, mouseY     int
	mouseIn, pointerOn bool
	pointer            string
	// dropdown is the card menu a right-click opened (dropdown.go).
	dropdown *dropdownState
}

// JiraConfig is the Jira client the jira: and ui: options make, for the
// terminal and the browser alike.
func JiraConfig(cfg config.JiraConfig, ui config.UIConfig) jira.Config {
	opts, _ := optionsFrom(ui)
	timeout, _ := cfg.RequestTimeout()
	return jira.Config{
		BaseURL:          cfg.BaseURL,
		Email:            cfg.Email,
		APIToken:         cfg.APIToken,
		SignIn:           cfg.Credentials(),
		Projects:         cfg.Projects,
		StoryPointsField: cfg.StoryPointsField,
		CardLimit:        opts.cardLimit,
		FlagValue:        ui.FlagValue,
		InboxIssues:      opts.inboxIssues,
		Timeout:          timeout,
		FlatReplies:      !opts.threaded,
		CustomFields:     ui.CustomFields,
	}
}

// New builds the app from the jira: config.
func New(ctx context.Context, cfg config.JiraConfig, ui config.UIConfig, rs []rules.Rule, rulesLog string, st *store.Store) Model {
	opts, warn := optionsFrom(ui)
	keys := defaultKeys()
	warn = append(warn, keys.applyKeys(ui.Keys)...)
	th, thWarn := themeFrom(ui.Theme)
	applyTheme(th)
	setCodeTheme(opts.codeTheme)
	plainIcons = opts.plainIcons
	warn = append(warn, thWarn...)
	ruleSet, ruleWarn := rules.Compile(rs)
	warn = append(warn, ruleWarn...)
	actions, actWarn := actionsFrom(ui.Actions, &keys)
	warn = append(warn, actWarn...)
	if _, err := cfg.RequestTimeout(); err != nil {
		warn = append(warn, err.Error())
	}
	prompt := defaultJiraStartPrompt
	if cfg.StartPrompt != "" {
		prompt = cfg.StartPrompt
	}
	m := Model{
		ctx:             ctx,
		store:           st,
		keys:            keys,
		pointerOn:       opts.mouse && pointerOK(),
		jiraClient:      jira.New(JiraConfig(cfg, ui)),
		jiraProjects:    append([]string(nil), cfg.Projects...),
		jiraRepos:       cfg.Repos,
		jiraStartPrompt: prompt,
		jiraStartStatus: cfg.StartStatuses,
		jiraTab:         newJiraTabState(),
		inbox:           &inboxData{}, // Init syncs it on a copy: the pointer must be there already
		images:          newPanelImages(opts.images, opts.imageMaxRows),
		opts:            opts,
		uiConfig:        ui,
		rules:           ruleSet,
		actions:         actions,
		rulesLog:        rulesLog,
		herdr:           herdr.Default(),
		refView:         viewport.New(),
		fieldCursor:     -1,
		started:         time.Now(),
	}
	m.refView.SoftWrap = true
	m.startupStatus(warn)
	m.jiraTab.wantLanes = opts.lanes
	m.loadPins()
	m.loadPanelWidth()
	if m.store != nil {
		_, m.jiraTab.compact, _ = m.store.GetMeta(jiraCompactMeta)
		m.jiraTab.emptyLanes, _, _ = m.store.GetMeta(jiraEmptyLanesMeta)
		v, _, _ := m.store.GetMeta(createMoreMeta)
		m.createMore = v == "true"
		m.jiraClient.SetQueue(queueTo(m.store))
		m.jiraClient.SetLayouts(m.store)
		m.fieldPins = jira.FieldPins(m.store)
		m.queued = len(readQueue(m.store))
	}
	return m
}

// WithIndex mirrors the issues the app reads into ix, which the palette
// searches and the panel falls back on offline (index.go).
func (m Model) WithIndex(ix *index.Index) Model {
	m.index = ix
	if ix != nil {
		m.jiraClient.SetIndex(ix)
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.enterJiraTab(), m.jiraAutoRefreshTick(), m.followSiteEvents(), m.queryCellSize(), m.startRuleWatches(), m.timerStart(), m.syncInbox(), m.inboxTick(), loadingTick(), m.detectBranchIssue(), m.fetchAgents(), queueTick(), m.checkRelease(), resendImagesLater())
}

// bodyH is the rows above the status line.
func (m *Model) bodyH() int {
	return max(m.height-1, 5)
}

// resize lays the board and the panel out for the current size.
func (m *Model) resize() {
	bodyH := m.bodyH()
	m.sizeJiraView(m.width, bodyH-1)
	_, refW := m.jiraListWidth(m.width)
	m.refView.SetWidth(max(refW-4, 1))
	m.sizeRefView()
	if m.agentTerm != nil {
		m.agentTerm.resize(m.agentTermSize())
	}
	m.renderJira()
	m.renderRef()
	m.fitImageView()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if b, ok := msg.(wheelBatchMsg); ok {
		return m.wheelBatch(b)
	}
	wasLoading := m.jiraTab != nil && m.jiraTab.loading // the tab is shared: read it before
	out, cmd := m.update(msg)
	if om, ok := out.(Model); ok {
		om.logStatus()
		if om.status != m.status {
			om.statusAt = time.Now()
		}
		if _, tick := msg.(loadingTickMsg); !tick && om.jiraTab != nil && om.jiraTab.loading && !wasLoading {
			cmd = tea.Batch(cmd, loadingTick()) // a load began: its elapsed time ticks
		}
		out = om
		// The inline picker is drawn in the panel: redraw it as it changes.
		if _, motion := msg.(tea.MouseMotionMsg); !motion && (m.pickerInline() || om.pickerInline() || m.panelComposing() || om.panelComposing()) {
			om.renderRef()
			if _, wheel := msg.(tea.MouseWheelMsg); !wheel { // the wheel scrolls away from the cursor
				om.showInlinePicker()
				om.showInlineEditor()
			}
			out = om
		}
		if om.dropdownOn() { // the rows a dropdown shows stay put until the selection leaves them
			om.jiraPicker.top, _ = om.dropdownWindow(om.dropdownWin(om.bodyH()))
			out = om
		}
		if f := om.flushImages(); f != nil {
			return om, tea.Batch(cmd, f)
		}
	}
	return out, cmd
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		termBG = msg.Color
		adaptTheme(msg.Color)
		autoShade(msg.Color)
		m.sizeRefView() // shaded, the trail drops its rule
		m.renderRef()
		m.jiraTab.rows = nil
		m.renderJira()
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleClick(msg)
	case tea.MouseMotionMsg:
		if m.agentTermDrag {
			m.agentTermDragTo(msg.Mouse(), false)
			return m, nil
		}
		if m.panelResizing {
			return m.resizePanel(msg.X)
		}
		if m.panelScrolling {
			m.scrollPanelTo(msg.Y)
			return m, nil
		}
		if r := m.jiraTab.roadmap; r != nil && r.drag.on && msg.Button == tea.MouseLeft {
			return m.dragRoadmap(msg.X)
		}
		if p := m.jiraTab.plan; p != nil && p.drag.held && msg.Button == tea.MouseLeft {
			return m.dragPlan(msg.X, msg.Y)
		}
		if m.jiraTab.headDrag.col != "" && msg.Button == tea.MouseLeft {
			return m.dragJiraHead(msg.X)
		}
		if m.jiraDragging() && msg.Button == tea.MouseLeft {
			return m.dragJira(msg.X, msg.Y)
		}
		if m.panelSel.held && msg.Button == tea.MouseLeft {
			return m.dragPanelSel(msg.X, msg.Y)
		}
		m.mouseX, m.mouseY, m.mouseIn = msg.X, msg.Y, true
		if m.dropdownOn() { // a menu's row follows the pointer
			if i, _ := m.pickerRowAt(msg.X, msg.Y); i >= 0 {
				m.jiraPicker.idx = i
			}
		}
		return m, m.pointerTo(m.hoverAt(msg.X, msg.Y).pointer)
	case tea.MouseReleaseMsg:
		if m.agentTermDrag {
			m.agentTermDragTo(msg.Mouse(), true)
			return m, nil
		}
		if m.panelSel.held {
			return m.endPanelSel()
		}
		if m.panelResizing {
			m.panelResizing = false
			m.savePanelWidth()
			return m, nil
		}
		m.panelScrolling = false
		if r := m.jiraTab.roadmap; r != nil {
			r.drag = roadmapDrag{}
		}
		if p := m.jiraTab.plan; p != nil && p.drag.held {
			return m.dropPlan()
		}
		if m.jiraTab.headDrag.col != "" {
			return m.dropJiraHead()
		}
		if msg.Button == tea.MouseLeft && m.jiraDragging() {
			return m.dropJira()
		}
		return m, nil
	case tea.MouseWheelMsg:
		return m.handleWheel(msg)
	case tea.PasteMsg:
		if m.agentTermTyping() && !m.modalOpen() {
			m.agentTerm.paste(msg.Content)
			return m, nil
		}
		if m.descEdit != nil {
			var cmd tea.Cmd
			m.descEdit.input, cmd = m.descEdit.input.Update(msg)
			return m, cmd
		}
		if m.jiraCommentActive {
			var cmd tea.Cmd
			m.jiraCommentInput, cmd = m.jiraCommentInput.Update(msg)
			return m, tea.Batch(cmd, m.scheduleMention())
		}
		if m.jiraFieldActive {
			var cmd tea.Cmd
			m.jiraFieldInput, cmd = m.jiraFieldInput.Update(msg)
			if m.fieldInline() {
				m.renderRef()
			}
			return m, cmd
		}
		return m.pasteInput(msg)

	case jiraBoardMsg:
		out, cmd := m.handleJiraBoard(msg)
		mm := out.(Model)
		return mm, tea.Batch(cmd, mm.fetchAvatars(), mm.fetchCardColors())
	case jiraCardsMsg:
		out, cmd := m.handleJiraCards(msg)
		mm := out.(Model)
		return mm, tea.Batch(cmd, mm.fetchAvatars())
	case avatarLoadedMsg:
		return m.handleAvatarLoaded(msg)
	case cardColorsMsg:
		return m.handleCardColors(msg)
	case jiraRankedMsg:
		return m.handleJiraRanked(msg)
	case jiraMovedMsg:
		return m.handleJiraMoved(msg)
	case jiraLoadedMsg:
		return m.handleJiraLoaded(msg)
	case jiraGotoMsg:
		return m.handleJiraGoto(msg)
	case activityLoadedMsg:
		return m.handleActivityLoaded(msg)
	case jiraDownloadedMsg:
		return m.handleJiraDownloaded(msg)
	case jiraVoteMsg:
		return m.handleJiraVote(msg)
	case jiraWatchMsg:
		return m.handleJiraWatch(msg)
	case descLoadedMsg:
		return m.handleDescLoaded(msg)
	case descConflictMsg:
		return m.handleDescConflict(msg)
	case descEditedMsg:
		out, cmd := m.handleDescEdited(msg)
		return out, tea.Batch(cmd, resendImagesLater())
	case proposalsMsg:
		return m.handleProposals(msg)
	case linkSearchMsg:
		return m.handleLinkSearch(msg)
	case linkFoundMsg:
		return m.handleLinkFound(msg)
	case timeMachineMsg:
		return m.handleTimeMachine(msg)
	case llmDoneMsg:
		return m.handleLLMDone(msg)
	case confettiMsg:
		return m.handleConfetti(msg)
	case notesEditedMsg:
		out, cmd := m.handleNotesEdited(msg)
		return out, tea.Batch(cmd, resendImagesLater())
	case mentionSearchMsg:
		return m.handleMentionSearch(msg)
	case mentionFoundMsg:
		return m.handleMentionFound(msg)
	case jqlWordsMsg:
		return m.handleJQLWords(msg)
	case jqlValuesMsg:
		return m.handleJQLValues(msg)
	case inboxTickMsg:
		return m.handleInboxTick()
	case gitlabCheckMsg:
		return m.handleGitLabCheck(msg)
	case inboxSyncMsg:
		return m.handleInboxSync(msg)
	case draftSaveMsg:
		return m.handleDraftSave()
	case pullRequestMsg:
		return m.handlePullRequest(msg)
	case commentVisMsg:
		return m.handleCommentVis(msg)
	case filterSavedMsg:
		return m.handleFilterSaved(msg)
	case editUndoneMsg:
		return m.handleEditUndone(msg)
	case batchCreatedMsg:
		return m.handleBatchCreated(msg)
	case actionDoneMsg:
		return m.handleActionDone(msg)
	case agentTickMsg:
		return m, m.fetchAgents()
	case queueTickMsg:
		return m.handleQueueTick()
	case queueReplayedMsg:
		return m.handleQueueReplayed(msg)
	case agentsMsg:
		return m.handleAgents(msg)
	case agentIssuesMsg:
		return m.handleAgentIssues(msg)
	case agentTermDueMsg:
		return m.handleAgentTermDue(msg)
	case agentAttachedMsg:
		out, cmd := m.handleAgentAttached(msg)
		return out, tea.Batch(cmd, resendImagesLater())
	case agentDoneMsg:
		return m.handleAgentDone(msg)
	case termOutputMsg:
		return m.handleTermOutput(msg.t, false)
	case termExitMsg:
		return m.handleTermOutput(msg.t, true)
	case paletteSearchMsg:
		return m.handlePaletteSearch(msg)
	case paletteFoundMsg:
		return m.handlePaletteFound(msg)
	case prefetchMsg:
		return m.handlePrefetch(msg)
	case worklogLoggedMsg:
		return m.handleWorklogLogged(msg)
	case worklogFailedMsg:
		return m.handleWorklogFailed(msg)
	case timerTickMsg:
		return m.handleTimerTick()
	case standupMsg:
		return m.handleStandup(msg)
	case standupTickMsg:
		return m.handleStandupTick(msg)
	case weekAddMsg:
		return m.handleWeekAdd(msg)
	case weekMsg:
		return m.handleWeek(msg)
	case chartsMsg:
		return m.handleCharts(msg)
	case planMsg:
		return m.handlePlan(msg)
	case planSprintMsg:
		return m.handlePlanSprint(msg)
	case planWroteMsg:
		return m.handlePlanWrote(msg)
	case bulkMoveMsg:
		return m.handleBulkMove(msg)
	case bulkDoneMsg:
		return m.handleBulkDone(msg)
	case roadmapMsg:
		return m.handleRoadmap(msg)
	case roadmapSaveMsg:
		return m.handleRoadmapSave(msg)
	case roadmapSavedMsg:
		return m.handleRoadmapSaved(msg)
	case panelExtraMsg:
		return m.handlePanelExtra(msg)
	case jiraPickerLoadedMsg:
		return m.handleJiraPickerLoaded(msg)
	case jiraAssigneeDebounceMsg:
		return m.handleJiraAssigneeDebounce(msg)
	case jiraMutatedMsg:
		return m.handleJiraMutated(msg)
	case branchIssueMsg:
		return m.handleBranchIssue(msg)
	case releaseMsg:
		m.handleRelease(msg)
		return m, nil
	case labelTickMsg:
		return m.handleLabelTick(msg)
	case labelsFoundMsg:
		return m.handleLabelsFound(msg)
	case jiraDeletedMsg:
		return m.handleJiraDeleted(msg)
	case jiraRelocatedMsg:
		return m.handleJiraRelocated(msg)
	case jiraPreparedMsg:
		return m.handleJiraPrepared(msg)
	case jiraFormDoneMsg:
		return m.handleJiraFormDone(msg)
	case jiraWorkMsg:
		return m.handleJiraWork(msg)
	case resendImagesMsg:
		return m.handleResendImages()
	case imageLoadedMsg:
		return m.handleImageLoaded(msg)
	case jiraAutoRefreshMsg:
		return m.handleJiraAutoRefresh()
	case jiraSiteChangeMsg:
		return m.handleSiteChange(msg)
	case jiraEventRefreshMsg:
		return m.handleEventRefresh()
	case jiraEventInboxMsg:
		return m.handleEventInbox()
	case jiraSiteEventsEndMsg:
		return m, nil
	case uv.CellSizeEvent:
		return m.handleCellSize(msg)
	case createFieldsMsg:
		return m.handleCreateFields(msg)
	case jiraCloneDraftMsg:
		return m.handleJiraCloneDraft(msg)
	case jiraCreateTypesMsg:
		return m.handleJiraCreateTypes(msg)
	case jiraCreatedMsg:
		return m.handleJiraCreated(msg)
	case ruleWatchMsg:
		return m, m.pollRuleWatch(msg.jql)
	case ruleWatchedMsg:
		return m.handleRuleWatched(msg)
	case rulesEventsMsg:
		return m.handleRulesEvents(msg)
	case ruleActedMsg:
		return m.handleRuleActed(msg)
	case rulesLoggedMsg:
		if msg.err != nil {
			m.fail(i18n.Tf("rule: %s", msg.err.Error()))
		}
		return m, nil
	case loadingTickMsg:
		return m.handleLoadingTick()
	case startWritesMsg:
		return m.handleStartWrites(msg)
	case pageMsg:
		return m.handlePage(msg)
	case mrMsg:
		return m.handleMR(msg)
	case mrInboxMsg:
		return m.handleMRInbox(msg)
	case diffLoadedMsg:
		return m.handleDiffLoaded(msg)
	case jobLogMsg:
		return m.handleJobLog(msg)
	case jobLogTickMsg:
		return m.handleJobLogTick(msg)
	case mrPipelineTickMsg:
		return m.handleMRPipelineTick(msg)
	case diffExpandedMsg:
		return m.handleDiffExpanded(msg)
	case diffNotePostedMsg:
		return m.handleDiffNotePosted(msg)
	case diffResolvedMsg:
		return m.handleDiffResolved(msg)
	case diffReviewedMsg:
		return m.handleDiffReviewed(msg)
	case mrWroteMsg:
		return m.handleMRWrote(msg)
	case mrDescEditedMsg:
		return m.handleMRDescEdited(msg)
	case mrReviewMsg:
		return m.handleMRReview(msg)
	case openedMsg:
		if msg.err != nil {
			m.fail(i18n.Tf("open %s: %s", msg.name, msg.err.Error()))
		}
		return m, nil
	}
	return m, nil
}

// handleKey routes a key to the modal that owns the keyboard, else the
// focused pane.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() != "ctrl+c" && !key.Matches(msg, m.keys.Quit) {
		m.quitAsked = false
	}
	if m.dragging() && msg.String() == "esc" {
		return m.cancelDrag()
	}
	switch {
	case m.jobLog != nil && m.settings == nil && !m.helpOpen:
		return m.handleJobLogKey(msg)
	case m.diff != nil && m.settings == nil && !m.helpOpen:
		return m.handleDiffKey(msg)
	case m.settings != nil:
		return m.handleSettingsKey(msg)
	case m.filterBuilder != nil:
		return m.handleFilterBuilderKey(msg)
	case m.descEdit != nil:
		return m.handleDescEditKey(msg)
	case m.helpOpen:
		return m.handleHelpKey(msg)
	case m.imageView:
		return m.handleImageViewKey(msg)
	case m.jql != nil:
		return m.handleJQLKey(msg)
	case m.jiraGotoActive:
		return m.handleJiraGotoKey(msg)
	case m.jiraPicker.active:
		return m.handleJiraPickerKey(msg)
	case m.jiraFieldActive:
		return m.handleJiraFieldKey(msg)
	case m.jiraCommentActive:
		return m.handleJiraCommentKey(msg)
	case m.jiraForm != nil:
		return m.handleJiraFormKey(msg)
	case m.focus == focusRef && m.agentTermShown():
		return m.handleAgentTermKey(msg)
	case m.agentTermTyping() && m.agentsTermShown():
		return m.handleAgentsKey(msg)
	case m.focus == focusRef && m.refOpen:
		return m.handleRefKey(msg)
	case m.jiraTab.searching:
		return m.handleJiraSearchKey(msg)
	}
	before := m.selectedJiraKey()
	out, cmd := m.handleJiraKey(msg)
	if om, ok := out.(Model); ok && om.jiraTab.roadmap == nil && om.jiraTab.plan == nil && om.jiraTab.charts == nil && om.jiraTab.week == nil && om.jiraTab.standup == nil && om.jiraTab.inbox == nil && om.jiraTab.agentsView == nil && om.jiraTab.mrs == nil {
		if after := om.selectedJiraKey(); after != "" && after != before {
			return om, tea.Batch(cmd, om.schedulePrefetch())
		}
	}
	return out, cmd
}

// inlineEditorOnly is whether the one thing open is an editor drawn inside
// the panel, which leaves the panel's wheel free.
func (m *Model) inlineEditorOnly() bool {
	inline := m.jiraCommentActive && m.commentInline() || m.jiraFieldActive && m.fieldInline() || m.descEdit != nil && m.descEditInline()
	return inline && m.settings == nil && m.filterBuilder == nil && !m.helpOpen && !m.imageView && m.jql == nil && !m.jiraGotoActive &&
		!m.jiraPicker.active && m.jiraForm == nil &&
		(m.descEdit == nil || m.descEditInline()) && (!m.jiraCommentActive || m.commentInline()) && (!m.jiraFieldActive || m.fieldInline())
}

func (m *Model) modalOpen() bool {
	return m.settings != nil || m.filterBuilder != nil || m.descEdit != nil || m.helpOpen || m.imageView || m.jql != nil || m.jiraGotoActive || m.jiraPicker.active || m.jiraFieldActive || m.jiraCommentActive || m.jiraForm != nil
}

func (m Model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.diff != nil || m.jobLog != nil { // keys only: nothing under it takes a click
		return m, nil
	}
	if msg.Button == tea.MouseLeft && m.pickerInline() {
		return m.clickInlinePicker(msg.X, msg.Y)
	}
	if msg.Button == tea.MouseLeft && m.pickerOnTop() {
		// A click picks the row (as enter would); outside the box, cancels.
		switch i, outside := m.pickerRowAt(msg.X, msg.Y); {
		case outside && m.dropdown != nil:
			// On the menu under a submenu: back to it, and that row; else
			// off both, they close.
			if d := m.dropdown; d.parent != "" && m.backToMenu() {
				if i, _ := m.pickerRowAt(msg.X, msg.Y); i >= 0 {
					m.jiraPicker.idx = i
					return m.handleJiraPickerKey(keyPress("enter"))
				}
				return m, nil
			}
			m.closeJiraPicker()
			return m, nil
		case outside:
			return m.handleJiraPickerKey(keyPress("esc"))
		case i == pickerApplyRow:
			m.jiraPicker.ticked = true // the ticks as shown, even none
			return m.handleJiraPickerKey(keyPress("enter"))
		case i >= 0 && m.jiraPicker.checked != nil:
			m.jiraPicker.idx = i // several at once: a click ticks
			m.toggleChecked()
			return m, nil
		case i >= 0:
			m.jiraPicker.idx = i
			return m.handleJiraPickerKey(keyPress("enter"))
		}
		return m, nil
	}
	if m.helpOpen && m.settings == nil && m.filterBuilder == nil && m.descEdit == nil {
		m.helpOpen, m.helpPage = false, 0 // any click closes it
		return m, nil
	}
	if msg.Button == tea.MouseRight && m.cardClickable(msg.X, msg.Y) {
		return m.rightClickJira(m.hitJira(msg.X, msg.Y), msg.X, msg.Y)
	}
	form := m.formOnTop()
	if msg.Button != tea.MouseLeft || msg.Y >= m.bodyH() {
		return m, nil
	}
	count := 1
	if time.Since(m.lastClick.at) < m.opts.doubleClick && m.lastClick.x == msg.X && m.lastClick.y == msg.Y {
		count = 2
	}
	m.lastClick.at, m.lastClick.x, m.lastClick.y = time.Now(), msg.X, msg.Y
	if count == 2 {
		m.lastClick.at = time.Time{}
	}
	if out, cmd, ok := m.clickOverlay(msg.X, msg.Y, count); ok {
		return out, cmd
	}
	if m.modalOpen() && !form {
		return m, nil
	}
	if form {
		return m.clickJiraForm(msg.X, msg.Y, count)
	}
	if listW, _ := m.jiraListWidth(m.width); m.refOpen && msg.X == listW {
		m.panelResizing, m.panelResizeFrom = true, m.opts.panelPct // the panel's left border: drag to resize
		return m, nil
	}
	if m.refOpen && !m.agentTermShown() && msg.X == m.width-1 && m.onPanelScrollbar(msg.Y) {
		m.panelScrolling, m.panelScrollFrom = true, m.refView.YOffset() // the scrollbar: jump there, drag to scroll
		m.scrollPanelTo(msg.Y)
		return m, nil
	}
	if listW, _ := m.jiraListWidth(m.width); m.agentTermShown() && msg.X > listW {
		return m.clickAgentPanel(msg)
	}
	if listW, _ := m.jiraListWidth(m.width); m.refOpen && msg.X >= listW {
		m.focus = focusRef
		m.startPanelSel(msg.X, msg.Y)
		if i := m.crumbAt(msg.Y); i >= 0 {
			return m.backToCrumb(i)
		}
		if u := m.panelLinkAt(msg.X, msg.Y); u != "" {
			return m.clickPanel(panelHit{field: -1, url: u}, count)
		}
		if i := m.panelLineAt(msg.Y); i >= 0 && i == m.activityLine && m.jiraIssue != nil {
			listW, _ := m.jiraListWidth(m.width)
			line := strings.Split(m.refView.GetContent(), "\n")[i]
			if t := activityTabAt(line, msg.X-listW-1, max(m.jiraIssue.CommentTotal, len(m.jiraIssue.Comments))); t >= 0 {
				return m, m.switchActivity(t)
			}
		}
		if i, wrap := m.panelCellAt(msg.Y); i >= 0 {
			if h, ok := m.panelHits[i]; ok {
				h.col = msg.X - listW - 1 + wrap*m.refView.Width() - panelIndent(strings.Split(m.refView.GetContent(), "\n")[i])
				return m.clickPanel(h, count)
			}
		}
		m.renderJira()
		return m, nil
	}
	if out, cmd, ok := m.clickHeader(msg.X, msg.Y); ok {
		return out, cmd
	}
	switch t := m.jiraTab; {
	case t.roadmap != nil:
		m.focus = focusJira
		return m.clickRoadmap(msg.X, msg.Y, count)
	case t.plan != nil:
		m.focus = focusJira
		return m.clickPlan(msg.X, msg.Y, count)
	case t.inbox != nil:
		return m.clickInbox(msg.X, msg.Y, count)
	case t.agentsView != nil:
		return m.clickAgents(msg, count)
	case t.mrs != nil:
		return m, nil
	case t.charts != nil, t.week != nil, t.standup != nil:
		return m, nil
	case t.empty.row >= 0 && msg.Y == jiraBodyTop+t.empty.row:
		if out, cmd, ok := m.runSeg(segAt(t.empty.segs, msg.X, 1+t.empty.left)); ok {
			return out, cmd
		}
	}
	return m.clickJira(m.hitJira(msg.X, msg.Y), msg.X, msg.Y, count)
}

func (m Model) handleWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	if j := m.jobLog; j != nil {
		switch msg.Button {
		case tea.MouseWheelUp:
			j.scroll(-3)
		case tea.MouseWheelDown:
			j.scroll(3)
		}
		return m, nil
	}
	if d := m.diff; d != nil {
		switch msg.Button {
		case tea.MouseWheelUp:
			d.move(-3)
		case tea.MouseWheelDown:
			d.move(3)
		}
		return m, nil
	}
	if (m.agentTermShown() || m.agentsTermShown()) && !m.modalOpen() && m.agentTermWheel(msg) {
		return m, nil
	}
	if m.pickerOnTop() {
		switch msg.Button {
		case tea.MouseWheelUp:
			return m.handleJiraPickerKey(keyPress("up"))
		case tea.MouseWheelDown:
			return m.handleJiraPickerKey(keyPress("down"))
		}
		return m, nil
	}
	if msg.Button == tea.MouseWheelUp || msg.Button == tea.MouseWheelDown {
		if out, cmd, ok := m.wheelOverlay(msg.Button == tea.MouseWheelUp); ok {
			return out, cmd
		}
	}
	if m.modalOpen() && !m.inlineEditorOnly() {
		return m, nil
	}
	delta := 3
	if msg.Button == tea.MouseWheelUp {
		delta = -3
	} else if msg.Button != tea.MouseWheelDown {
		return m, nil
	}
	if listW, _ := m.jiraListWidth(m.width); m.refOpen && msg.X >= listW {
		m.refView.SetYOffset(m.refView.YOffset() + delta)
		return m, nil
	}
	if m.inlineEditorOnly() {
		return m, nil // the board stays put under an edit
	}
	if t := m.jiraTab; t.roadmap != nil || t.plan != nil {
		m.wheelView(msg.X, delta/3)
		return m, nil
	}
	if m.jiraShowsLanes() {
		// Over another lane the wheel scrolls it; the cursor's lane (and
		// swimlanes, which scroll together) moves the cursor.
		t := m.jiraTab
		if h := m.hitJira(msg.X, msg.Y); h.idx >= 0 && h.idx != t.lane && t.swim == jiraSortRank && h.idx < len(t.laneTop) {
			t.laneTop[h.idx] = min(max(t.laneTop[h.idx]+delta/3, 0), max(len(t.lanes[h.idx].cards)-1, 0))
			m.renderJira()
			return m, nil
		}
		m.moveJiraCursor(delta / 3)
		return m, nil
	}
	m.jiraTab.view.SetYOffset(m.jiraTab.view.YOffset() + delta)
	return m, nil
}

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if m.opts.mouse {
		v.MouseMode = tea.MouseModeAllMotion
	}
	if m.width == 0 {
		return v
	}
	bodyH := m.bodyH()
	body := m.renderJiraPane(bodyH, m.width)
	if m.diff != nil {
		body = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, m.renderDiffView(bodyH))
		if note := m.renderDiffNote(); note != "" {
			body = m.overDiff(body, note)
		}
	}
	if m.jobLog != nil {
		body = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, m.renderJobLog(bodyH))
	}
	if m.imageView {
		body = m.renderImageView(m.width, bodyH)
	}
	var ov string
	if m.dropdownOn() && !m.imageView {
		body = m.drawDropdown(body, bodyH)
	} else if ov = m.renderOverlay(bodyH); ov != "" {
		body = lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, ov)
	}
	body = m.placeCompletion(body, ov)
	st := statusStyle
	if m.statusIsErr() {
		st = refErrStyle
	}
	status := st.Render(ansi.Truncate(" "+m.shownStatus(time.Now()), m.width, "…"))
	screen, ok := joinVerticalLeft(body, status)
	if !ok {
		screen = lipgloss.JoinVertical(lipgloss.Left, body, status)
	}
	if m.mouseIn {
		screen = underline(screen, m.hoverAt(m.mouseX, m.mouseY))
	}
	v.SetContent(screen)
	if cx, cy, ok := m.diffNoteCursor(m.renderDiffNote()); ok {
		v.Cursor = tea.NewCursor(cx, cy)
	} else if cx, cy, ok := m.agentTermCursor(); ok {
		v.Cursor = tea.NewCursor(cx, cy)
	} else if cx, cy, ok := m.inlineEditorCursor(); ok {
		v.Cursor = tea.NewCursor(cx, cy)
	} else if m.descEdit != nil && !m.descEditInline() {
		if cx, cy, ok := m.modalComposerCursor(0, &m.descEdit.input, ov); ok {
			v.Cursor = tea.NewCursor(cx, cy)
		}
	}
	if m.jiraCommentActive && !m.commentInline() {
		above := 0
		if m.jiraCommentReplyTo != "" {
			above = 1
		}
		if cx, cy, ok := m.modalComposerCursor(above, &m.jiraCommentInput, ov); ok {
			v.Cursor = tea.NewCursor(cx, cy)
		}
	}
	return v
}

// pickerOnTop is whether the picker is the modal drawn (renderOverlay).
func (m *Model) pickerOnTop() bool {
	return m.jiraPicker.active && m.settings == nil && m.filterBuilder == nil && m.descEdit == nil && !m.helpOpen && m.jql == nil && !m.jiraGotoActive &&
		!m.jiraCommentActive && !m.jiraFieldActive
}

// formOnTop is whether the transition form is the modal drawn.
func (m *Model) formOnTop() bool {
	return m.jiraForm != nil && !m.jiraPicker.active && m.settings == nil && m.filterBuilder == nil && m.descEdit == nil && !m.helpOpen && !m.imageView && m.jql == nil && !m.jiraGotoActive &&
		!m.jiraCommentActive && !m.jiraFieldActive
}

// renderOverlay draws the open modal, last one winning as in matterbox.
func (m *Model) renderOverlay(bodyH int) string {
	switch {
	case m.settings != nil:
		return m.renderSettings(bodyH)
	case m.filterBuilder != nil:
		return m.renderFilterBuilder(bodyH)
	case m.descEdit != nil && !m.descEditInline():
		return m.renderDescEdit()
	case m.helpOpen:
		return m.renderHelp(bodyH)
	case m.jql != nil:
		return m.renderJQL()
	case m.jiraGotoActive:
		return m.renderJiraGoto()
	case m.jiraCommentActive && !m.commentInline():
		return m.renderJiraCommentInput()
	case m.jiraFieldActive && !m.fieldInline():
		return m.renderJiraFieldInput()
	case m.jiraPicker.active && !m.pickerInline():
		return m.renderJiraPicker(bodyH)
	case m.jiraForm != nil:
		return m.renderJiraForm()
	}
	return ""
}

// unsentWork is what quitting now would lose, "" for nothing: writes still
// on their way, an edited description, a comment you wrote.
func (m *Model) unsentWork() string {
	var what []string
	if n := m.jiraClient.Writing(); n == 1 {
		what = append(what, i18n.T("a write is still sending"))
	} else if n > 1 {
		what = append(what, i18n.Tf("%d writes are still sending", n))
	}
	if d := m.descEdit; d != nil && d.input.Value() != d.before {
		what = append(what, i18n.T("your edit is unsaved"))
	}
	if m.jiraCommentActive && strings.TrimSpace(m.jiraCommentInput.Value()) != strings.TrimSpace(m.jiraCommentBefore) {
		what = append(what, i18n.T("your comment is unsent"))
	}
	return strings.Join(what, " · ")
}

// quit leaves the app, asking once first when that would lose work.
func (m Model) quit() (tea.Model, tea.Cmd) {
	if w := m.unsentWork(); w != "" && !m.quitAsked {
		m.quitAsked = true
		m.status = i18n.Tf("%s · quit again to leave anyway (a comment or edit is kept as a draft)", w)
		return m, nil
	}
	m.saveOpenDrafts()
	return m, tea.Quit
}
