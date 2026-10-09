# Configuration

One file, `~/.config/laneway/config.yaml`, for the terminal and the
browser. The first `laneway` (or the setup page of `laneway web`) writes it,
readable only by you. Both settings screens (`,` in the terminal, `g ,` in
the browser) edit the `ui:` options in place, comments kept.

A bad value keeps its default and is reported; an unknown key warns with a
"did you mean". One value where a list goes (`projects: ABC`) is a list of
one. An existing `~/.config/jiratui` or matterbox config (its `jira:` and
`ui:`) is picked up as a fallback.

## Jira

```yaml
jira:
  base_url: https://your-instance.atlassian.net
  email: you@example.com
  name: Work              # how this site is labelled in the site picker and the web's header
  api_token: ...          # or JIRA_API_TOKEN (overrides it), or:
  # api_token_cmd: [secret-tool, lookup, service, laneway]   # prints the token (pass, op read, …)
  # sign_in: laneway      # a laneway-server: its own sign-in (laneway login) instead of email and api_token
  projects: [ABC]         # listed first in the project picker
  repos: {ABC: ~/src/abc} # checkouts: start work, your commits in the standup and worklog proposals
  start_statuses: {ABC: Doing}   # where start work moves ABC issues, over ui.start_status ("" for none)
  start_prompt: "Start on {key}."  # what start work hands the agent; default a built-in prompt, none for no prompt
  story_points_field: customfield_10016  # else found by name ("Story point…")
  timeout: 20s            # one request's limit (an attachment's: without progress)
sites:                    # more Jira instances, same keys as jira:
  club: {base_url: https://club.atlassian.net, email: you@example.com, api_token: ...}
gitlab:                   # GitLab instances for merge requests; a host not listed uses glab's login (glab auth login --hostname <host>)
  - base_url: https://git.example.com
    token: ...            # read_api; api to approve, merge, edit and comment. Or token_cmd: [...]
    repos: {group/project: ~/src/project}  # checkouts for an agent review; else found among jira.repos by origin
```

`laneway setup` adds a site under `sites:`; given a URL already there, it
replaces that site's email and token (an expired token). Where there is a
keyring (`secret-tool`, macOS Keychain) it offers to keep the token there,
writing only the command that reads it back. `@` (in the app) switches
sites and is remembered: the terminal, `laneway web`, `laneway prompt` and
completion start on the last one picked; the script commands, `rules` and
`hook` use `jira:` unless given `-site`.

laneway only reads your Jira until you act: moving a card, editing a field
or commenting.

## ui options

Every option is optional. `,` (terminal) and `g ,` (browser) list them by
topic with their value, default and what they do. Options marked
*restart* apply on the next start (the browser's Settings > Server >
Restart starts `laneway web` again); the rest apply at once.

### Board and cards

| Option | Default | |
| --- | --- | --- |
| `default_mode` | `lanes` | `lanes` or `list`, before one is remembered per board. *Restart* |
| `card_fields` | all | what cards and list rows show, in order: `type priority status points assignee avatar parent pr deploy subtasks due flagged age` |
| `card_layout` | | lane cards' fields around the summary: `top`, `top_right`, `bottom`, `bottom_right`; any of `card_fields` plus `key`, `labels` and custom fields |
| `card_styles` | | restyle the cards a `/` query matches: `when`, `edge`, `tint` (`accent ok warn err info` or `#rrggbb`), `fade`, `bold`, `hide`, `show`; a later match wins |
| `card_colors` | `ribbon` | the board's own card colours as a bar on cards and rows; `off` |
| `empty_lanes` | `show` | `hide`: lanes the filters leave empty |
| `lane_layouts` | | your own lanes over a board's columns, `alt+l` switches; see [Lane layouts](#lane-layouts) |
| `custom_fields` | | Jira fields by name on cards and rows, searchable as `"test type":e2e`. *Restart* |
| `card_limit` | `500` | most cards one view fetches (50–5000); a view, search or planning side it cuts off says how many Jira has. Charts and the roadmap count up to 5000. *Restart* |
| `kanban_done_days` | `14` | done work older than this leaves kanban boards |
| `stale_days` | `5` | an in-progress card's age turns red past this |
| `flag_value` | `Impediment` | the Flagged option flag sets. *Restart* |
| `icons` | `nerd` | issue type glyphs need a Nerd Font; `plain` draws letters. Terminal |

```yaml
ui:
  card_layout:
    top: [type, key, flagged]
    top_right: [status, points]
    bottom: [parent, due, labels]
    bottom_right: [age, avatar]
  card_styles:
    - {when: "prio>=high", edge: err, bold: true}
    - {when: "is:done", fade: true}
    - {when: "-due<7d", hide: [due]}   # show: keeps fields off cards it doesn't match
```

### Views and filters

| Option | Default | |
| --- | --- | --- |
| `home` | | start on the home screen with these widgets, in order: `work inbox sprint timer filters` |
| `quick_filters` | | `{name, jql}` presets before the board's own |
| `board_quick_filters` | `on` | the board's own quick filters from Jira; `off`: only `quick_filters` |
| `views` | | `{name, jql}` views of every board, after its own |
| `saved_filters` | `on` | your starred Jira filters and service desk queues as views |
| `remember_filters` | `on` | the assignee filter (mine) and each board's quick filters stay on next time |
| `filters` | | `{name, query}`: named `/` queries, recalled from the palette |
| `my_work_jql` | yours, open or resolved this week | the query of My work |

```yaml
ui:
  quick_filters: [{name: Bugs, jql: "type = Bug"}]
  views: [{name: Mine, jql: "assignee = currentUser()"}]
  filters: [{name: Stale review, query: "status:review age>3d"}]
```

### Panel

| Option | Default | |
| --- | --- | --- |
| `panel_width` | `50` | the issue panel's share of the width in percent (20–80); dragging its border remembers another |
| `empty_fields` | `show` | `hide` folds empty fields behind a row |
| `date_format` | `2006-01-02 15:04` | a Go time layout; within a week the panel says `2d ago` |
| `images` | `auto` | inline images in kitty and Ghostty; `off`. Terminal, *restart* |
| `image_max_rows` | `16` | the tallest an inline image gets (1–200). Terminal, *restart* |
| `templates` | | a new issue's description by type, markdown |
| `code_theme` | the theme's | a chroma style for code blocks. Terminal |
| `threaded_replies` | `on` | reply in Jira's thread; `off` posts a new comment quoting it. *Restart* |
| `comment_order` | `oldest` | `newest` puts the latest comment first; a thread keeps its replies oldest first |
| `comment_layout` | `threaded` | `flat` lists comments by date, a reply under a truncated line quoting its parent |

```yaml
ui:
  templates:
    Bug: "## Steps\n\n1. \n\n## Expected\n\n## Actual"
```

### Refresh

| Option | Default | |
| --- | --- | --- |
| `auto_refresh` | `2m` | how often an idle board refetches; `off` |
| `stale_after` | `1m` | an older board refetches on focus |
| `full_refresh` | `10m` | idle refreshes fetch changes only, the whole board again after this. Terminal |

### Time and worklogs

| Option | Default | |
| --- | --- | --- |
| `timer_round` | to the minute | logged timer time rounded up to this (`15m`; up to 8h) |
| `timer_on_start` | `off` | start work also starts the timer |
| `workday_start` | `09:00` | when work logged on another day starts |
| `workdays` | mon–fri | for the standup's previous workday and the week's gaps |
| `capacity` | | sprint points per person, `{Ada: 13, default: 10}` |
| `activity` | | commands printing `time<TAB>KEY what` lines of a day, for worklog proposals |
| `calendar` | | an iCalendar file, https or webcal URL, or a vdir: meetings are proposed as worklogs and taken off your capacity. Needs `meeting_key` |
| `meeting_key` | | the issue meetings are logged on |

### Start work and agents

| Option | Default | |
| --- | --- | --- |
| `start_assigns` | `off` | start work also assigns the issue to you |
| `start_status` | | and moves it there (`In Progress`), unless the move has a screen |
| `branch_template` | `{key}-{summary}` | the copied branch name: `{key} {summary} {type} {project}` |
| `work_branch_template` | `issue/{key}-{summary}` | start work's new branch; `branch_template` when that is set |
| `work_agent` | `claude` | the herdr agent start work offers first |
| `work_args` | | its arguments before the prompt; `{key}` is replaced |
| `work_create` | | the command that makes a missing worktree (`{branch} {base} {key}`); unset: herdr |
| `agent_view` | `fullscreen` | `panel` attaches an agent in the panel. Terminal |
| `llm` | `claude -p` when on the PATH | the ask command: the question last, the issue on stdin (`llm`, `ollama run llama3`) |
| `actions` | | your own commands on an issue, see [Actions](#actions) |

```yaml
ui:
  work_args: [--append-system-prompt, "User is working on {key}"]
  work_create: [wt, switch, --create, "{branch}", --base, "{base}", --no-cd]
```

### Planning, roadmap and charts

| Option | Default | |
| --- | --- | --- |
| `velocity_sprints` | `8` | closed sprints in the velocity chart (1–50) |
| `report_done` | Jira's resolution | the column the charts count done from, by board id: `{"12": In review}` is that column and every one right of it. `d` on the charts sets it |
| `report_backwards` | `live` | an issue moved back before the done line: `live` stops counting it, `first` counts it from its first crossing |
| `roadmap_epic_type` | `Epic` | the issue type the roadmap shows and creates (`Initiative`, …) |
| `roadmap_done_days` | `90` | resolved epics older than this leave the roadmap |

### Inbox

| Option | Default | |
| --- | --- | --- |
| `inbox_every` | `5m` | inbox sync, for the unread count; `off` |
| `inbox_lookback` | `168h` | how far back the inbox reaches |
| `inbox_issues` | `30` | recently updated issues the inbox and standup read (1–200). *Restart* |

### Standup

| Option | Default | |
| --- | --- | --- |
| `standup_start` | `everyone` | `first`: open on the first person |
| `standup_lookback` | `1` | workdays back the standup starts (1–10); a Monday covers Friday |
| `standup_length` | `15m` | the whole standup, split over the people for each turn |
| `standup_timebox` | | each person's turn instead of the split |
| `standup_shuffle` | `off` | go round in a random order |
| `standup_timer` | `off` | a timer for each turn and the whole standup |

### Look and feel

| Option | Default | |
| --- | --- | --- |
| `theme` | the terminal's colours | see [Terminal theme](#terminal-theme); the browser has its own under Settings › Appearance |
| `mouse` | `on` | `off` leaves the mouse to the terminal. Terminal |
| `double_click` | `400ms` | how quickly a second click opens (100ms–2s). Terminal |
| `keys` | | rebind actions by name: [terminal](terminal-keys.md#rebinding), [browser](browser-keys.md#rebinding) |
| `web_keys` | | rebind browser keys without a terminal action, by id: [browser](browser-keys.md#rebinding). Browser |
| `delight` | `on` | small celebrations: confetti on a card into done, a sprint's points against the last ones |
| `language` | `en` | the language laneway shows: `en` or `nl`; `LANEWAY_LANG` overrides it |
| `skin_tone` | | the emoji tone for people: `light medium_light medium medium_dark dark` |

### System

| Option | Default | |
| --- | --- | --- |
| `open` | `xdg-open` / `open` | opens URLs and attachments (`wslview`) |
| `clipboard_image` | probed | prints the clipboard's PNG (`wl-paste --type image/png`) |
| `download_dir` | `$XDG_DOWNLOAD_DIR`, else `~/Downloads` | where attachments are saved |
| `update_check` | `on` | a daily look for a newer release, `↑ v1.2` in the header |

## Lane layouts

`ui.lane_layouts` are your own lanes over a board's columns: in your order,
several columns stacked in one lane under a header each, a column's
statuses split over lanes, renamed, some hidden. `alt+l` steps through the board's own columns and each layout that
fits it, remembered per board; nothing changes in Jira.

The browser's settings draw a layout over a board's real columns: drag a
column onto a lane, between lanes or onto Hidden, split a column (its
button, or `s`) to place its statuses one by one, drag lanes to reorder,
type their names, tick the boards it is on or *Every board it fits*. In
the terminal `alt+L` arranges the board's layout in place (a new one when
it shows none), `s` splitting a column there too. Both write the config
for you; by hand:

A lane lists statuses by id, so boards on the same workflow share a
layout. A column whose statuses are in one lane is stacked there whole; one
whose statuses are spread over lanes is a section in each, named by its
statuses there. A status a layout doesn't list stays with its column's
first listed one. A layout fits a board where it places at least two
columns; `site:` keeps it to the Jira it was made on (the editors write
it: another site's status ids mean other statuses), `boards:` to those
board ids. A column it doesn't place
keeps a lane of its own after its left neighbour's; `hidden:` takes
statuses off the board, and the header counts their cards.

```yaml
ui:
  lane_layouts:
    - name: Dev
      site: acme.atlassian.net    # optional
      boards: [12, 34]            # optional
      lanes:
        - statuses: ["10000"]     # To do; the name defaults to the column's
        - name: Doing
          statuses: ["3", "10020"]          # In progress, Blocked
        - name: Done
          statuses: ["10010", "10011", "10012", "10001"]  # Test, UAT, Deploy, Done
      hidden: ["10030"]           # Won't do
```

A lane's limit is its columns' limits added up, when each has one; a
split column's limit counts in none. `H` and
`L` step a card through a stacked lane's columns one by one; a column of
several statuses asks which one; a drag shows a drop zone per status. `z` folds a
stacked column, remembered per board.

## Actions

`ui.actions` are your own commands on an issue: each is in the palette, and
on its key when it has one no built-in uses. It runs with the selected
issue (in the panel, the panel's; on the board, the marked cards when there
are) as JSON on stdin, `LANEWAY_KEY` and `LANEWAY_KEYS` in the
environment. The output's last line shows as a message, or all of it with
`show: pager`; `refresh: true` reloads the board and issue after. `where:`
is `board`, `panel` or `both`.

```yaml
ui:
  actions:
    - {name: open in my notes, key: "!", command: [sh, -c, 'obsidian "jira/$LANEWAY_KEY"']}
    - {name: estimate, command: [./estimate.sh], where: panel, show: pager, refresh: true}
```

In the browser they run on the machine `laneway web` runs on.

## Terminal theme

```yaml
ui:
  theme:
    preset: tokyonight
    accent: "#7aa2f7"
    dim: "244"
```

`theme: gruvbox` alone works too. Colours take ANSI `0`–`255`, `#rrggbb` or
`#rgb`.

- Dark presets: tokyonight, tokyonight-storm, catppuccin,
  catppuccin-macchiato, catppuccin-frappe, gruvbox, dracula, nord,
  solarized-dark, onedark, rosepine, rosepine-moon, kanagawa,
  kanagawa-dragon, monokai.
- Light presets: tokyonight-day, catppuccin-latte, gruvbox-light,
  solarized-light, rosepine-dawn, kanagawa-lotus, onelight.
- `mono` (or `NO_COLOR` set) draws no colour at all: the cursor and a drop
  in reverse, the accent bold, the dim faint, code blocks plain.

Colour names: `accent dim selection_fg selection_bg selection_idle error
mention link code attachment over_limit drop_fg priority_highest
priority_high priority_low priority_lowest type_bug type_story type_epic
type_subtask type_other type_alert highlight roadmap_done roadmap_todo
status_todo status_progress status_done`, and `shade`.

- `status_*` colour the lane marks and the panel's status lozenges; unset
  they follow `selection_bg`, `roadmap_todo` and `roadmap_done`.
- `type_*` colour issue type icons by Jira's colour for the icon: red
  `type_bug`, green `type_story`, purple `type_epic`, blue `type_other`,
  orange `type_alert` (unset it follows `highlight`).
- `shade`: `auto` steps off the terminal's own background (faint for the
  canvas, list zebra rows; stronger for the panel's trail, section bars,
  the board title, lane heads and status line), `off`, or one colour for
  all.
- On a light terminal the default `selection_idle` turns light grey (`253`).

## Files

| File | Holds |
| --- | --- |
| `~/.config/laneway/config.yaml` | this configuration |
| `~/.config/laneway/state.json` | last project, board, view and filters, cached boards, the timer, drafts, the offline queue, starred queries; `state-club.json` for a site |
| `~/.config/laneway/notes/ABC-12.md` | private notes; `notes-club/` for a site |
| `~/.config/laneway/rules.log` | what `log` rule actions wrote |
| `~/.cache/laneway/index-jira.db` | the local index of every issue read; `index-club.db` for a site |
| `~/.cache/laneway/avatars` | avatar images |

### Signing in to a laneway-server

A site on [laneway-server](https://github.com/cornedor/laneway-server)
can sign in the way its web pages do (password and two-step, passkey,
SSO, GitLab) instead of with an API token: `laneway setup` and the setup
page offer it, `laneway login` does it later. The config then says
`sign_in: laneway`; the sign-in itself (a key made for it and a refresh
token) lives in the system keyring, or without one in
`~/.config/laneway/sign-in/` readable only by you. Its tokens last 15
minutes, renew themselves and only work with that key (OAuth with DPoP),
so a copied one is of no use. `laneway logout` ends it on the server too.
