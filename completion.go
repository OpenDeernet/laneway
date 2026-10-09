package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/cornedor/laneway/internal/config"
	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/store"
	"github.com/cornedor/laneway/internal/ui"
)

// laneway completion bash|zsh|fish prints a script that asks laneway
// __complete for the words: subcommands, flags, -site names and, for
// view and move, the issue keys on the boards laneway last loaded.

var completionUsage = i18n.N("usage: laneway completion bash|zsh|fish   (e.g. source <(laneway completion bash))")

var completionScripts = map[string]string{
	"bash": `_laneway() {
  local IFS=$'\n'
  COMPREPLY=($(laneway __complete "${COMP_WORDS[@]:1:COMP_CWORD}"))
}
complete -o default -F _laneway laneway
`,
	"zsh": `#compdef laneway
_laneway() {
  local -a c
  c=("${(@f)$(laneway __complete "${(@)words[2,CURRENT]}")}")
  compadd -a c
}
# Autoloaded from fpath (the packages' site-functions) this file is the
# body of _laneway: complete now. Sourced, register it.
if [ "$funcstack[1]" = "_laneway" ]; then
  _laneway "$@"
else
  compdef _laneway laneway
fi
`,
	"fish": `complete -c laneway -f -a '(laneway __complete (commandline -opc)[2..-1] (commandline -ct))'
`,
}

// commandFlags are each command's flags, for completion.
var commandFlags = map[string][]string{
	"":       {"-config", "-site", "-demo", "-version"},
	"list":   {"-config", "-site", "-format", "-jql"},
	"view":   {"-config", "-site", "-format"},
	"create": {"-config", "-site", "-format", "-project", "-type", "-summary", "-description"},
	"move":   {"-config", "-site"},
	"prompt": {"-config", "-site", "-format"},
	"hook":   {"-config", "-site", "-strict", "-force"},
	"rules":  {"-config", "-site", "-on", "-key", "-summary", "-type", "-status", "-from-status", "-assignee", "-priority", "-points", "-watch", "-by-me"},
	"setup":  {"-config", "-site"},
	"mr":     {"-config"},
	"token":  {"-config", "-site", "-days"},
	"apps":   {"-config", "-site"},
}

var commandArgs = map[string][]string{
	"hook":       {"install"},
	"index":      {"clear"},
	"rules":      {"list", "test", "watch"},
	"token":      {"list", "create", "revoke", "revoke-all"},
	"apps":       {"list", "revoke"},
	"completion": {"bash", "zsh", "fish"},
}

func completionCmd(args []string, out, errOut io.Writer) int {
	if len(args) != 1 || completionScripts[args[0]] == "" {
		fmt.Fprintln(errOut, i18n.T(completionUsage))
		return 2
	}
	fmt.Fprint(out, completionScripts[args[0]])
	return 0
}

// completeCmd prints the words that complete the last of args, one a line.
func completeCmd(args []string, out io.Writer) int {
	if len(args) == 0 {
		args = []string{""}
	}
	cur, before := args[len(args)-1], args[:len(args)-1]
	cmd, cfgPath, site, positional := "", "", "", 0
	for i := 0; i < len(before); i++ {
		switch w := before[i]; {
		case w == "-config" || w == "-site" || (strings.HasPrefix(w, "-") && slices.Contains(commandFlags[cmd], w) && w != "-strict" && w != "-force" && w != "-version"):
			if i+1 < len(before) {
				if w == "-config" {
					cfgPath = before[i+1]
				}
				if w == "-site" {
					site = before[i+1]
				}
				i++
			}
		case strings.HasPrefix(w, "-"):
		case cmd == "":
			cmd = w
		default:
			positional++
		}
	}
	var words []string
	prev := ""
	if len(before) > 0 {
		prev = before[len(before)-1]
	}
	switch {
	case prev == "-site":
		if cfg, _, err := config.Load(cfgPath); err == nil {
			words = slices.DeleteFunc(cfg.SiteNames(), func(s string) bool { return s == "" })
		}
	case prev == "-format":
		words = []string{"plain", "csv", "json"}
	case strings.HasPrefix(cur, "-"):
		words = commandFlags[cmd]
	case cmd == "":
		words = []string{"list", "view", "create", "move", "prompt", "hook", "rules", "index", "setup", "mr", "token", "apps", "completion"}
	case (cmd == "view" || cmd == "move") && positional == 0:
		words = cachedKeys(cfgPath, site)
	case positional == 0:
		words = commandArgs[cmd]
	}
	for _, w := range words {
		if strings.HasPrefix(w, cur) {
			fmt.Fprintln(out, w)
		}
	}
	return 0
}

// cachedKeys are the issue keys on the site's stored boards.
func cachedKeys(cfgPath, site string) []string {
	if site == "" {
		if cfg, _, err := config.Load(cfgPath); err == nil {
			site = config.LastSite(cfg.SiteNames())
		}
	}
	path, err := config.SiteStatePath(site)
	if err != nil {
		return nil
	}
	st, err := store.Open(path)
	if err != nil {
		return nil
	}
	return ui.CachedKeys(st)
}
