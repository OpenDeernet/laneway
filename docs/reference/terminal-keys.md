# Terminal keys

Every key of `laneway`, by screen, as bound by default. `?` shows them as
bound on your machine, paged with `←` `→`; from the panel it opens on the
panel's page. Keys for the browser: [Browser keys](browser-keys.md).

## Everywhere

| Key | Does |
| --- | --- |
| `?` | every key, as bound |
| `:` | the palette: actions of the focused pane, views, quick filters, boards, loaded and recent issues; from three letters all of Jira and the local index (`⌕`). Its `messages` row lists the last status-line messages |
| `#` | go to an issue by key or pasted URL |
| `,` | settings |
| `@` | switch or add a Jira site |
| `~` | home |
| `q` | quit; on the roadmap, planning, charts, the standup or the week it closes them |

## Board

| Key | Does |
| --- | --- |
| arrows, `h` `j` `k` `l` | move |
| `g` `G`, `pgup` `pgdn` (`ctrl+u` `ctrl+d`) | top, bottom, a page |
| `enter` | open the issue in the panel |
| `tab` | focus the panel |
| `v` | show or hide the panel |
| `<` `>` | widen, narrow the panel |
| `/` | search the loaded cards ([syntax](search.md)) |
| `F` | filter builder: field, compare and value columns; `enter` adds the term, `ctrl+x` drops the last |
| `m` | mine |
| `a` | assignee filter (`space` or `tab` ticks several, `enter` applies) |
| `1`–`9`, `0` | quick filters, clear every filter |
| `Q` | JQL search |
| `p`, `b` | project, board |
| `[` `]` | previous, next view |
| `t` | lanes or list |
| `s` | list: sort (rank, priority, points, assignee, epic, key, status, updated, due, created); lanes: swimlanes by assignee, epic or priority |
| `z`, `Z` | fold the band (or the stacked lane's section), unfold all |
| `{`, `}` | previous, next band; in a grouped list, group |
| `c` | one-line cards |
| `alt+e` | hide or show lanes the filters leave empty |
| `alt+l` | next lane layout (`ui.lane_layouts`), then the board's columns |
| `alt+L` | arrange the lanes: `h` `l` pick a column, `H` `L` stack it on the lane before or after, `n` own lane, `x` hide or show, `s` split a column into its statuses (or gather them), `<` `>` move its lane, `r` rename, `esc` done |
| `r` | refresh |

### Cards

| Key | Does |
| --- | --- |
| `H` `L` (`shift+←` `→`) | move a lane; in the list, to the previous, next column |
| `K` `J` | rank up, down; `alt+k` `alt+j` to the top, bottom |
| `M` | to a sprint or the backlog |
| `e` | quick edit: status, priority, assignee, labels, points, sprint |
| `x`, `X` | mark the card, the lane (every shown row in the list) |
| `B` | edit the marked cards |
| `u` | undo the last change, again the one before (up to 50) |
| `.` | repeat the last move or edit on this card |
| `n` | new issue |
| `*` | pin |
| `o` | open in the browser |
| `y` `Y` | copy key, URL; with marks in the list, `y` copies a markdown table |
| `ctrl+y` | copy a branch name |
| `T` | start or stop the timer |

### Screens

| Key | Opens |
| --- | --- |
| `O` | my work |
| `R` | roadmap |
| `P` | planning |
| `C` | charts |
| `V` | releases |
| `ctrl+e` | refine the view's open issues |
| `ctrl+t` | time machine: `←` `→` a day, `esc` back |
| `ctrl+o` | a closed sprint as it closed |
| `I` | inbox |
| `U` | standup |
| `W` | today's worklogs, again the week |
| `ctrl+g` | agents |
| `alt+m` | merge requests waiting on you |

## Panel

| Key | Does |
| --- | --- |
| `j` `k`, `space` `b` | scroll a line, a page |
| `tab` `shift+tab` | walk the fields; `enter` edits one, `*` pins or unpins it |
| `s` `p` `P` | status, priority, points |
| `e` `E` | summary, description |
| `l` `a` | labels, assignee |
| `c` | comment; `ctrl+s` posts, `ctrl+o` sets who sees it |
| `R` | reply |
| `}` `{` | select a comment: `R` replies, `enter` edits yours, `delete` twice deletes |
| `[` `]` | activity tabs: comments, history, work log, all |
| `H` | history |
| `D` | development: pull requests, builds, deployments, branches, commits |
| `L` | open a link, subtask, child, parent or web link |
| `backspace` | back to the issue a link came from |
| `A` | more: subtasks, links, clone, type, move, estimate, delete, watchers, votes, flag, files, pull request, worktree |
| `w` | log work |
| `T` | timer |
| `S` | start work, or attach to its agent |
| `N` | private notes in `$EDITOR` |
| `ctrl+a` | ask `ui.llm` about the issue |
| `/`, `n` `N` | find in the issue, next, previous hit |
| `i` | images full size |
| `*` | pin |
| `o`, `y` `Y`, `ctrl+y` | browser, copy key, URL, branch |
| `r` | refresh |
| `esc` | drop the field, close |

## Forms

The create, move and bulk forms: `tab` `↓` `ctrl+n` next field, `shift+tab`
`↑` `ctrl+p` the one before; `ctrl+s` submits from any row, `esc` cancels.
On create, `←` `→` change the type, `enter` on the summary creates,
`alt+enter` (or `ctrl+enter`) creates and keeps the form.

## Planning

| Key | Does |
| --- | --- |
| `←` `→` | switch side |
| `[` `]` | another sprint |
| `x`, `M` or `space` | mark, move the marked (or selected) across |
| `K` `J` | rank |
| `S` | start the sprint, or move an active one's end |
| `R` `E` | rename, edit the goal |
| `N` | new sprint |
| `C` twice | complete the active sprint |
| `e` `B` | quick edit, edit the marked |
| `/`, `F` | filter both sides, filter builder |
| `u` | undo the last move across |
| `y` | copy the sprint as a markdown table |

## Roadmap

| Key | Does |
| --- | --- |
| `←` `→`, `+` `-`, `.` | scroll, zoom, today |
| `space` | fold an epic's issues |
| `enter` | open |
| `H` `L` | move the bar |
| `<` `>` | move its end |
| `e` | grip its start, again its end, again let go; `h` `l` move it |
| `u` | put the last moved bar back |
| `E` | quick edit |
| `f` | the epic's issues as a board view |
| `n` | new epic |
| `/` | filter |
| `y` | copy as a markdown table |

## Charts

`tab` the next chart, `y` copies its numbers as a markdown table. `d`
picks the column done counts from (`ui.report_done`), `c` sets a second
line beside it until you leave (again removes it).

## Inbox

| Key | Does |
| --- | --- |
| `tab` | Inbox, Mentions, All |
| `e`, `E` | done until news, every read thread done |
| `s` | snooze till the next workday |
| `u` | unread |
| `enter`, `o` | open in the panel, in the browser |
| `c` `R` | comment, reply |

## Standup

| Key | Does |
| --- | --- |
| `←` `→` | previous, next person |
| `a` | who takes part |
| `s` | shuffle |
| `space` | start, pause the timer (`ui.standup_timer`) |
| `P` | park the card |
| `z`, `enter` on Off the board | fold it out |
| `[` `]` | a workday back, forward |
| `enter` | open the issue |
| `y` | copy the stop as markdown, keys linked |
| `esc` `U` | back |

## Worklogs

| Key | Does |
| --- | --- |
| `[` `]` | a day (the week: a week) |
| `e` `d` | edit, delete twice |
| `x` `X` | mark an entry, every entry: `d` and `m` take the marked |
| `m` | move to another issue (Jira Cloud) |
| `p` | propose missing worklogs |
| `enter` | open; on a proposal, log it; in the week, log on the cell |
| `#` | the week: add an issue's row |
| `y` | copy as a markdown table |
| `W` | the week, and back |

## Agents

| Key | Does |
| --- | --- |
| `enter` | type into the agent's terminal; `ctrl+\` back |
| `tab` | worktrees without an agent too |
| `v` | open the issue |
| `p` | send a prompt |
| `d` twice | stop it |

## Merge requests

The list (`alt+m`): `enter` reads one in the panel, `d` its diff, `o`
GitLab, `r` refresh. In the panel: `i` the Jira issue it names, `p` its
pipeline's jobs (`enter` a job's log, `G` follows it), `A` approve, `M`
merge, `e` edit (title, draft, reviewers, assignees, labels, target branch,
description in `$EDITOR`), `C` agent review. Without a token for its GitLab
it says how to sign in; `r` tries again.

The diff:

| Key | Does |
| --- | --- |
| `tab` | files or diff |
| `]` `[`, `n` `N` | next file, next thread |
| `c` | note on the line, or reply to the thread |
| `V` | mark a range for `c` |
| `s` | suggest a change |
| `E` `x` | reword, drop a pending note |
| `S` | submit the review: comment, approve or request changes |
| `A` | approve |
| `M` | merge |
| `C` | agent review |
| `R` | resolve, reopen |
| `e` | the whole file, again the changes |
| `z` `Z` | fold |
| `←` `→` | pan |
| `v` | an earlier push |
| `r`, `esc` | reload, back |

## Mouse

`ui.mouse: off` leaves the mouse to the terminal.

- A click selects, a second opens (`ui.double_click`). What a click acts on
  is underlined, and the pointer turns to a hand where the terminal draws
  pointer shapes.
- Drag a card to another lane, or along its own to rank it; it lands where
  its ghost shows. `esc` cancels.
- Right-click a card or row: its menu at the pointer.
- The header clicks: views, filters, chips, key hints, the timer, `✉`; the
  sprint bar opens the charts.
- In the panel: drag over text to copy it, drag the left border to resize,
  click a field to select it and again to edit; links, tabs, images and a
  comment's byline click.

## Rebinding

`ui.keys` rebinds an action to one key or a list:

```yaml
ui:
  keys:
    search: f
    mine: [m, M]
    roadmap: none     # unbound
```

A key bound to two actions on one screen is reported. The actions:

| Screen | Actions |
| --- | --- |
| Board | `up down left right top bottom page_up page_down open toggle_panel browser refresh quit help search goto create copy_key copy_url copy_branch move_left move_right rank_up rank_down rank_top rank_bottom move_sprint project board next_view prev_view toggle_mode sort fold unfold_all next_band prev_band compact empty_lanes lane_layout arrange_lanes assignee_filter mine clear_filters filter_builder jql mark mark_all undo bulk quick_edit repeat refine pin palette roadmap plan charts releases timer timesheet inbox standup my_work review closed_sprint time_machine agents merge_requests home site settings panel_wider panel_narrower` |
| Panel | `status priority points summary labels assign description comment reply next_comment prev_comment delete_comment log_work start_work linked_issue back image issue_actions history development notes ask` |
| Agent terminal | `agent_back` |
| Planning | `plan_start plan_goal plan_rename plan_new plan_complete` |
| Charts | `chart_done chart_compare` |
| Roadmap | `roadmap_grip roadmap_fold end_earlier end_later zoom_in zoom_out today roadmap_issues roadmap_edit` |
| Worklogs | `edit_entry delete_entry move_entry propose_work` |
| Inbox | `inbox_done inbox_done_all inbox_unread inbox_snooze` |
| Agents | `agent_prompt agent_stop` |
| Standup | `standup_pause standup_shuffle standup_park` |
