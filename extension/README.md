# laneway redirect

Browser extension: Jira links open in `laneway web`. laneway's own Open in Jira (`o`, the card menu) still opens Jira.

Install: Chrome `chrome://extensions` → Developer mode → Load unpacked → this folder. Firefox (142+): `about:debugging` → Load Temporary Add-on → `manifest.json`.

Options: the laneway URL (default `http://127.0.0.1:8484`) and the Jira hosts, one per line, `host = site` to switch laneway to that site (`jira` is the `jira:` block). Empty: every `*.atlassian.net`, on laneway's current site. The toolbar button turns it off and on.

| Jira | laneway |
| --- | --- |
| `/browse/ABC-1`, `/issues/ABC-1`, `…/projects/ABC/issues/ABC-1` | `#/issue/ABC-1` |
| `/browse/ABC-1?focusedCommentId=10001` (a comment's link) | `#/issue/ABC-1?comment=10001`: Comments tab, scrolled to it, highlighted |
| `/browse/ABC`, `…/projects/ABC` (summary, board, list) | `#/board/ABC` |
| `…/projects/ABC/boards/12?selectedIssue=ABC-1` | `#/board/ABC/12?issue=ABC-1` |
| `…/boards/12/backlog`, `RapidBoard.jspa?view=planning` | `#/planning/ABC/12` |
| `…/boards/12/reports/velocity-chart` (burndown, burnup, cumulative, control, retrospective, release) | `#/reports/velocity/ABC/12` |
| `…/timeline`, `…/roadmap` | `#/roadmap/ABC` |
| `…/projects/ABC/releases` | `#/reports/releases/ABC` |
| `/issues/?jql=…`, `?filter=10042`, `?filter=-1` (system filters) | the board with the query as a view (`?sprint=jql:…`) |
| `…/projects/ABC/issues?jql=…` | the same, scoped to ABC |
| `/jira/your-work`, `/jira/for-you` | `#/work` |

Anything else (dashboards, Confluence, service desk, all issues of every project) stays in Jira.

How: a `declarativeNetRequest` rule sends main-frame Jira requests to `go.html`, which maps the URL (`map.js`) and moves on to laneway, or back to Jira with `?laneway=jira` (a rule lets that through). The rule skips requests started from laneway's host, from Jira itself and from Atlassian logins, so Jira you opened from laneway stays Jira. A Jira link in Confluence on the same site stays in Jira too.

Switching site sets laneway's `lw_site` cookie, which other open laneway tabs then follow as well.

Test: `node --test extension/*.test.mjs`.

Chrome Web Store: `extension/pack.sh`, then see [STORE.md](STORE.md).
