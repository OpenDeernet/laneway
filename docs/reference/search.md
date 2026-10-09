# Search

## The board's search

The board's search narrows the loaded cards as you type, without asking
Jira again: `/` in the terminal, `f` in the browser. Planning, the roadmap
and `ui.card_styles` use the same language. Every term must hold:

```
login                text in the key, summary, assignee or parent ("log in" a phrase)
status:review,test   a field containing any of the values
assignee:ada,bob     who: works too
epic:                a field that is empty (label:, points:, …; -epic: set)
points>2 prio>=high  points and priorities compare (<, <=, >, >=, =)
is:flagged           also done, pr, unassigned, mine, overdue, notes (has: too)
due<7d age>3d        due within a week, in progress over 3 days (h, d, w)
updated<1d           changed within a day (updated>7d: quiet a week)
created<7d           made within a week
reporter:bob         who reported it
component:api        one of its components
pr:open deploy:prod  pull request state, deployed environment
sprint:4             the sprint it is in now
"test type":e2e      a ui.custom_fields field by name
-label:ui            any term negated
```

Fields: `status`, `assignee` (`who`), `type`, `prio` (`priority`), `epic`
(`parent`), `label` (`labels`), `key`, `points` (`sp`), `due`, `age`,
`updated`, `created`, `reporter`, `component` (`components`), `pr`,
`deploy`, `sprint`, `is` (`has`), and `ui.custom_fields` quoted.

- `:` matches part of a value, `=` the whole of it.
- Spans take a whole number and `h`, `d` or `w`; `age` counts cards in
  progress only.
- `is:notes` finds issues with private notes.

The filter builder (`F` in both) writes these terms from three columns:
field, comparison and value. Each term shows as a chip; a click removes it.
`ui.filters` names queries to recall from the palette.

## JQL

`Q` asks Jira itself and shows the results as a view. It completes
fields, functions and keywords, and a field's values after an operator.

=== "Terminal"

    `tab` takes a suggestion, `↑` `↓` choose, `enter` runs it. An empty
    input offers your past searches. `ctrl+s` stars the query as a view on
    every board (`★ …`, again unstars it); `ctrl+f` saves it as a Jira
    filter under a name you type, starred.

    Offline, `Q` answers from the local index for its `AND`ed project, key,
    status, type, priority, assignee, statusCategory and `text ~` clauses,
    naming the ones it left out.

=== "Browser"

    `Q` opens the palette in JQL mode (`#` in the palette does the same).
    `tab` completes, `enter` lists the hits there. A hit opens beside them
    shown as a board view, `[` `]` in the panel stepping through them;
    `ctrl+enter` opens that view without one, from the query too. A reload
    keeps it. `ctrl+s` stars the query as a view on every board; `ctrl+f`
    saves it as a Jira filter.

`ui.saved_filters` (on) shows your starred Jira filters as views too, and
the service desks' queues (`DEMO · Open requests`); in the browser `#`
with nothing typed lists both.

## The palette

`:` searches every action, view, quick filter and board, the loaded issues
and the ones you opened lately, by every word you type. Actions match
common words too (`create`, `transition`, `worklog`). From three
characters it also searches all of Jira (summary, description, comments).

=== "Terminal"

    It also searches the local index of every issue laneway has read, in
    every project, offline too; those hits come last, marked `⌕`, with when
    they were read. Over the roadmap, planning or charts it lists that
    screen's actions only.

=== "Browser"

    The first character picks a mode: `:` commands, `/` issues, `#` JQL;
    `g ` jumps to a key. `ctrl+enter` opens an issue as a full page; on
    JQL hits, the hits as a view.
