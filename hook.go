package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
	"github.com/cornedor/laneway/internal/ui"
)

var hookUsage = i18n.N(`usage: laneway hook install [-strict] [-force]

Installs two git hooks in the repository you are in:
  commit-msg     a message without an issue key gets the branch's
                 (issue/ABC-12-fix: "ABC-12 fix it"); without one on the
                 branch either the commit is refused. -strict also refuses
                 keys Jira doesn't know.
  post-checkout  checking out an issue's branch while the issue is still to
                 do asks to move it in progress.
An existing hook that isn't laneway's is kept unless -force.`)

// hookMark tells laneway's hooks from others.
const hookMark = "# laneway hook"

// issueKeyRe finds issue keys in a commit message.
var issueKeyRe = regexp.MustCompile(`\b[A-Z][A-Z0-9_]+-[0-9]+\b`)

// hookCmd installs the hooks, or runs one of them as git calls it.
func hookCmd(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, i18n.T(hookUsage))
		return 2
	}
	fs := flag.NewFlagSet("hook "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	fs.Usage = func() { fmt.Fprintln(errOut, i18n.T(hookUsage)) }
	cfgPath := fs.String("config", "", i18n.T("config file"))
	site := fs.String("site", "", i18n.T("Jira site from the config's sites:"))
	strict := fs.Bool("strict", false, i18n.T("commit-msg: refuse keys Jira doesn't know"))
	force := fs.Bool("force", false, i18n.T("install: replace hooks that aren't laneway's"))
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	switch args[0] {
	case "install":
		return hookInstall(*strict, *force, *cfgPath, *site, out, errOut)
	case "commit-msg":
		if fs.NArg() != 1 {
			fmt.Fprintln(errOut, "laneway hook commit-msg:", i18n.T("the message file, as git passes it"))
			return 2
		}
		return hookCommitMsg(fs.Arg(0), *strict, *cfgPath, *site, errOut)
	case "post-checkout":
		// git passes the old head, the new one and 1 for a branch checkout.
		if fs.NArg() == 3 && fs.Arg(2) == "1" {
			hookPostCheckout(*cfgPath, *site, errOut)
		}
		return 0
	}
	fmt.Fprintln(errOut, i18n.T(hookUsage))
	return 2
}

// hookInstall writes the hooks into the repository's hooks directory.
func hookInstall(strict, force bool, cfgPath, site string, out, errOut io.Writer) int {
	dirOut, err := exec.Command("git", "rev-parse", "--git-path", "hooks").Output()
	if err != nil {
		fmt.Fprintln(errOut, "laneway hook install:", i18n.T("not in a git repository"))
		return 1
	}
	dir := strings.TrimSpace(string(dirOut))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(errOut, "laneway hook install:", err)
		return 1
	}
	var global []string
	if cfgPath != "" {
		global = append(global, "-config", shellQuote(cfgPath))
	}
	if site != "" {
		global = append(global, "-site", shellQuote(site))
	}
	flags := strings.Join(global, " ")
	msgFlags := flags
	if strict {
		msgFlags = strings.TrimSpace(msgFlags + " -strict")
	}
	hooks := map[string]string{
		"commit-msg":    fmt.Sprintf("laneway hook commit-msg %s \"$1\"", msgFlags),
		"post-checkout": fmt.Sprintf("laneway hook post-checkout %s \"$@\"", flags),
	}
	code := 0
	for _, name := range []string{"commit-msg", "post-checkout"} {
		path := filepath.Join(dir, name)
		if b, err := os.ReadFile(path); err == nil && !strings.Contains(string(b), hookMark) && !force {
			fmt.Fprintf(errOut, "laneway hook install: %s\n", i18n.Tf("%s is someone else's hook, kept (-force replaces it)", path))
			code = 1
			continue
		}
		body := "#!/bin/sh\n" + hookMark + ": `laneway hook install` wrote it\n" +
			"command -v laneway >/dev/null 2>&1 || exit 0\nexec " + strings.Join(strings.Fields(hooks[name]), " ") + "\n"
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			fmt.Fprintln(errOut, "laneway hook install:", err)
			return 1
		}
		fmt.Fprintln(out, i18n.Tf("installed %s", path))
	}
	return code
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// hookCommitMsg adds the branch's key to the message in path, or refuses it.
func hookCommitMsg(path string, strict bool, cfgPath, site string, errOut io.Writer) int {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(errOut, "laneway hook commit-msg:", err)
		return 1
	}
	msg, err := keyedMessage(string(b), ui.BranchIssue())
	if err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return 1
	}
	if strict {
		if err := checkKeys(cfgPath, site, messageKeys(msg)); err != nil {
			fmt.Fprintln(errOut, "laneway:", err)
			return 1
		}
	}
	if msg != string(b) {
		if err := os.WriteFile(path, []byte(msg), 0o644); err != nil {
			fmt.Fprintln(errOut, "laneway hook commit-msg:", err)
			return 1
		}
	}
	return 0
}

// keyedMessage is msg with branchKey before its first line when it names
// no issue; an error when neither does. Merges, fixups and empty messages
// pass as they are.
func keyedMessage(msg, branchKey string) (string, error) {
	lines := strings.Split(msg, "\n")
	first := slices.IndexFunc(messageLines(lines), func(l string) bool {
		return !strings.HasPrefix(l, "#") && strings.TrimSpace(l) != ""
	})
	if first < 0 || len(messageKeys(msg)) > 0 {
		return msg, nil
	}
	for _, p := range []string{"Merge ", "Revert ", "fixup! ", "squash! ", "amend! "} {
		if strings.HasPrefix(lines[first], p) {
			return msg, nil
		}
	}
	if branchKey == "" {
		return "", errors.New(i18n.T("no issue key in the message, nor in the branch name (issue/ABC-12-…)"))
	}
	lines[first] = branchKey + " " + lines[first]
	return strings.Join(lines, "\n"), nil
}

// messageLines are lines up to git's scissors, below which commit -v puts
// the diff.
func messageLines(lines []string) []string {
	for i, l := range lines {
		if strings.HasPrefix(l, "# ") && strings.Contains(l, ">8") {
			return lines[:i]
		}
	}
	return lines
}

// messageKeys are the issue keys in msg outside its comment lines.
func messageKeys(msg string) []string {
	var keys []string
	for _, l := range messageLines(strings.Split(msg, "\n")) {
		if !strings.HasPrefix(l, "#") {
			keys = append(keys, issueKeyRe.FindAllString(l, -1)...)
		}
	}
	return keys
}

// checkKeys refuses keys Jira doesn't know; one it can't ask about (no
// network) passes with a warning, so a commit offline still goes in.
func checkKeys(cfgPath, site string, keys []string) error {
	c, err := siteClient(cfgPath, site)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, k := range keys {
		if _, err := c.Get(ctx, k); errors.Is(err, jira.ErrNotFound) {
			return fmt.Errorf(i18n.T("%s is not an issue in Jira"), k)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "laneway: %s\n", i18n.Tf("couldn't check %s: %v", k, err))
		}
	}
	return nil
}

// siteClient is a client for the config's site.
func siteClient(cfgPath, site string) (*jira.Client, error) {
	cfg, _, err := loadSite(cfgPath, site)
	if err != nil {
		return nil, err
	}
	return jira.New(jira.Config{BaseURL: cfg.Jira.BaseURL, Email: cfg.Jira.Email, APIToken: cfg.Jira.APIToken, SignIn: cfg.Jira.Credentials(), Timeout: 10 * time.Second}), nil
}

// hookPostCheckout offers to move the branch's issue in progress while it
// is still to do. Without a terminal to ask on it says nothing; it never
// fails the checkout.
func hookPostCheckout(cfgPath, site string, errOut io.Writer) {
	key := ui.BranchIssue()
	if key == "" {
		return
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer tty.Close()
	c, err := siteClient(cfgPath, site)
	if err != nil {
		return
	}
	offerStart(context.Background(), c, key, tty, errOut)
}

// offerStart asks on rw to move key in progress, and moves it on a yes.
func offerStart(ctx context.Context, c *jira.Client, key string, rw io.ReadWriter, errOut io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	is, err := c.Get(ctx, key)
	if err != nil || is.StatusCategory != "new" {
		return
	}
	t, ok, err := c.StartTransition(ctx, key)
	if err != nil || !ok {
		return
	}
	fmt.Fprintf(rw, "laneway: %s ", i18n.Tf("move %s (%s) to %s? [y/N]", key, is.Status, t.Name))
	answer, _ := bufio.NewReader(rw).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" && a != "j" && a != "ja" {
		return
	}
	if err := c.DoTransition(ctx, key, t.ID); err != nil {
		fmt.Fprintln(errOut, "laneway:", err)
		return
	}
	fmt.Fprintf(rw, "laneway: %s\n", i18n.Tf("%s is %s", key, t.Name))
}
