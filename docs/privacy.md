# Privacy

laneway collects no data about you and sends nothing to its author. No
analytics, no tracking, no third parties beyond the services you connect it
to.

## On your own machine

The terminal app and `laneway web` talk only to the sites in your config
(Jira, Confluence, laneway-server, GitLab) and, if you set them up, the
commands, calendars and LLM you configure. What they keep stays on your
machine: your config, a state file with your choices and cached boards, and
your sign-in in the OS keyring or a file only you can read.

## laneway web for many people

`laneway web -hosted` runs on a server for a team. Whoever runs that server
is responsible for it; this section says what laneway itself does there.

**What it reads.** When you sign in (through laneway-server, or through
Atlassian for Jira Cloud) it gets your account id and access to the Jira
and Confluence sites you allow. It reads and changes issues and boards,
and reads Confluence pages, only when you do so in the app, as you, with
your permissions. Atlassian's consent screen shows the exact scopes.

**What it keeps**, in a directory of your own on that server:

- your sign-in (access and refresh tokens). On Jira Cloud they are
  encrypted with a key held outside that directory.
- your settings and a state file: remembered choices (views, filters,
  pins), drafts, edits waiting to be sent. Boards and issues are cached in
  your browser, not on the server.
- your session: a random value in an HttpOnly cookie in your browser; the
  server keeps only its hash, your account id and when it was made and last
  used. It ends after 12 hours idle, 7 days at most.

Your tokens never reach the browser. Nothing is shared with anyone else,
sold, or used for anything but showing you your work.

On Jira Cloud the server reports the account ids it keeps to Atlassian
once a week; for an account Atlassian says was closed, it deletes
everything it keeps of it.

**Signing out** ends the session and deletes the stored tokens. To
withdraw the app's access on Atlassian too, remove it under
[Connected apps](https://id.atlassian.com/manage-profile/apps). To have
your directory deleted, ask whoever runs the server.

## laneway.cd0.nl

The instance at laneway.cd0.nl is run by Corné Dorrestijn
([contact@corne.info](mailto:contact@corne.info)) as described above, on a
server in Germany (Hetzner). Ask there to have your data deleted.

Questions about laneway: [github.com/cornedor/laneway/issues](https://github.com/cornedor/laneway/issues).
