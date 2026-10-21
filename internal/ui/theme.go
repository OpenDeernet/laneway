package ui

import (
	"fmt"
	"image/color"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cornedor/laneway/internal/i18n"
)

// theme names the app's colours: ANSI numbers ("12") or hex ("#7aa2f7").
// ui.theme overrides them by name, over an optional preset.
type theme map[string]string

func defaultTheme() theme {
	return theme{
		"accent":           "12",  // keys, focus, active view
		"dim":              "241", // secondary text
		"selection_fg":     "15",
		"selection_bg":     "8",   // selected row, board focused
		"selection_idle":   "238", // selected row, panel focused
		"error":            "9",
		"mention":          "9",
		"link":             "12",
		"code":             "14",
		"attachment":       "6",
		"over_limit":       "9", // lane past its WIP limit
		"drop_fg":          "0", // drop target while dragging
		"priority_highest": "1",
		"priority_high":    "9",
		"priority_low":     "4",
		"priority_lowest":  "8",
		"type_bug":         "1", // issue type icons
		"type_story":       "2",
		"type_epic":        "5",
		"type_subtask":     "8",
		"type_other":       "4",    // task and the rest
		"type_alert":       "",     // question, problem, security…; "" follows highlight
		"highlight":        "11",   // a card a rule highlighted
		"roadmap_done":     "2",    // an epic bar's done part, the done status lozenge
		"roadmap_todo":     "4",    // and the rest (and in progress); today's line is highlight
		"status_todo":      "",     // lane marks and lozenges by status category; "" follows dim
		"status_progress":  "",     // "" follows roadmap_todo
		"status_done":      "",     // "" follows roadmap_done
		"shade":            "auto", // cards' faint background: auto (a step off the terminal's), off, or a colour
	}
}

// themePresets are full palettes for ui.theme: <name>, all for dark
// terminals. Missing names keep the ANSI default.
var themePresets = map[string]theme{
	// mono draws no colour: the cursor and selections in reverse, accents
	// bold, the dim faint. NO_COLOR picks it too.
	"mono": {},
	"tokyonight": {
		"accent": "#7aa2f7", "dim": "#565f89", "selection_fg": "#c0caf5",
		"selection_bg": "#33467c", "selection_idle": "#283457",
		"error": "#f7768e", "mention": "#f7768e", "link": "#7aa2f7", "code": "#7dcfff",
		"attachment": "#73daca", "over_limit": "#f7768e", "drop_fg": "#1a1b26",
		"priority_highest": "#db4b4b", "priority_high": "#ff9e64",
		"priority_low": "#7aa2f7", "priority_lowest": "#565f89",
		"type_bug": "#f7768e", "type_story": "#9ece6a", "type_epic": "#bb9af7",
		"type_subtask": "#565f89", "type_other": "#7aa2f7", "type_alert": "#ff9e64", "highlight": "#e0af68",
		"roadmap_done": "#9ece6a", "roadmap_todo": "#7aa2f7", "shade": "auto",
		"status_todo": "#565f89", "status_progress": "#7aa2f7", "status_done": "#9ece6a",
	},
	"catppuccin": { // mocha
		"accent": "#89b4fa", "dim": "#6c7086", "selection_fg": "#cdd6f4",
		"selection_bg": "#45475a", "selection_idle": "#313244",
		"error": "#f38ba8", "mention": "#f38ba8", "link": "#89b4fa", "code": "#94e2d5",
		"attachment": "#74c7ec", "over_limit": "#f38ba8", "drop_fg": "#11111b",
		"priority_highest": "#f38ba8", "priority_high": "#fab387",
		"priority_low": "#89b4fa", "priority_lowest": "#6c7086",
		"type_bug": "#f38ba8", "type_story": "#a6e3a1", "type_epic": "#cba6f7",
		"type_subtask": "#6c7086", "type_other": "#89b4fa", "type_alert": "#fab387", "highlight": "#f9e2af",
		"roadmap_done": "#a6e3a1", "roadmap_todo": "#89b4fa", "shade": "auto",
		"status_todo": "#6c7086", "status_progress": "#89b4fa", "status_done": "#a6e3a1",
	},
	"gruvbox": { // dark
		"accent": "#83a598", "dim": "#928374", "selection_fg": "#ebdbb2",
		"selection_bg": "#504945", "selection_idle": "#3c3836",
		"error": "#fb4934", "mention": "#fb4934", "link": "#83a598", "code": "#8ec07c",
		"attachment": "#8ec07c", "over_limit": "#fb4934", "drop_fg": "#282828",
		"priority_highest": "#cc241d", "priority_high": "#fe8019",
		"priority_low": "#83a598", "priority_lowest": "#928374",
		"type_bug": "#fb4934", "type_story": "#b8bb26", "type_epic": "#d3869b",
		"type_subtask": "#928374", "type_other": "#83a598", "type_alert": "#fe8019", "highlight": "#fabd2f",
		"roadmap_done": "#b8bb26", "roadmap_todo": "#83a598", "shade": "auto",
		"status_todo": "#928374", "status_progress": "#83a598", "status_done": "#b8bb26",
	},
	"dracula": { // dracula
		"accent": "#bd93f9", "dim": "#6272a4", "selection_fg": "#f8f8f2",
		"selection_bg": "#44475a", "selection_idle": "#343746",
		"error": "#ff5555", "mention": "#ff5555", "link": "#8be9fd", "code": "#50fa7b",
		"attachment": "#8be9fd", "over_limit": "#ff5555", "drop_fg": "#282a36",
		"priority_highest": "#ff5555", "priority_high": "#ffb86c",
		"priority_low": "#8be9fd", "priority_lowest": "#6272a4",
		"type_bug": "#ff5555", "type_story": "#50fa7b", "type_epic": "#bd93f9",
		"type_subtask": "#6272a4", "type_other": "#8be9fd", "type_alert": "#ffb86c", "highlight": "#f1fa8c",
		"roadmap_done": "#50fa7b", "roadmap_todo": "#8be9fd", "shade": "auto",
		"status_todo": "#6272a4", "status_progress": "#8be9fd", "status_done": "#50fa7b",
	},
	"nord": { // nord
		"accent": "#88c0d0", "dim": "#616e88", "selection_fg": "#eceff4",
		"selection_bg": "#434c5e", "selection_idle": "#3b4252",
		"error": "#bf616a", "mention": "#bf616a", "link": "#81a1c1", "code": "#8fbcbb",
		"attachment": "#88c0d0", "over_limit": "#bf616a", "drop_fg": "#2e3440",
		"priority_highest": "#bf616a", "priority_high": "#d08770",
		"priority_low": "#81a1c1", "priority_lowest": "#616e88",
		"type_bug": "#bf616a", "type_story": "#a3be8c", "type_epic": "#b48ead",
		"type_subtask": "#616e88", "type_other": "#81a1c1", "type_alert": "#d08770", "highlight": "#ebcb8b",
		"roadmap_done": "#a3be8c", "roadmap_todo": "#81a1c1", "shade": "auto",
		"status_todo": "#616e88", "status_progress": "#81a1c1", "status_done": "#a3be8c",
	},
	"onedark": { // onedark
		"accent": "#61afef", "dim": "#5c6370", "selection_fg": "#abb2bf",
		"selection_bg": "#3e4451", "selection_idle": "#2c313a",
		"error": "#e06c75", "mention": "#e06c75", "link": "#61afef", "code": "#56b6c2",
		"attachment": "#56b6c2", "over_limit": "#e06c75", "drop_fg": "#282c34",
		"priority_highest": "#e06c75", "priority_high": "#d19a66",
		"priority_low": "#61afef", "priority_lowest": "#5c6370",
		"type_bug": "#e06c75", "type_story": "#98c379", "type_epic": "#c678dd",
		"type_subtask": "#5c6370", "type_other": "#61afef", "type_alert": "#d19a66", "highlight": "#e5c07b",
		"roadmap_done": "#98c379", "roadmap_todo": "#61afef", "shade": "auto",
		"status_todo": "#5c6370", "status_progress": "#61afef", "status_done": "#98c379",
	},
	"rosepine": { // rosepine
		"accent": "#c4a7e7", "dim": "#6e6a86", "selection_fg": "#e0def4",
		"selection_bg": "#403d52", "selection_idle": "#26233a",
		"error": "#eb6f92", "mention": "#eb6f92", "link": "#9ccfd8", "code": "#31748f",
		"attachment": "#9ccfd8", "over_limit": "#eb6f92", "drop_fg": "#191724",
		"priority_highest": "#eb6f92", "priority_high": "#ebbcba",
		"priority_low": "#9ccfd8", "priority_lowest": "#6e6a86",
		"type_bug": "#eb6f92", "type_story": "#9ccfd8", "type_epic": "#c4a7e7",
		"type_subtask": "#6e6a86", "type_other": "#9ccfd8", "type_alert": "#f6c177", "highlight": "#f6c177",
		"roadmap_done": "#9ccfd8", "roadmap_todo": "#9ccfd8", "shade": "auto",
		"status_todo": "#6e6a86", "status_progress": "#9ccfd8", "status_done": "#9ccfd8",
	},
	"kanagawa": { // kanagawa
		"accent": "#7e9cd8", "dim": "#727169", "selection_fg": "#dcd7ba",
		"selection_bg": "#2d4f67", "selection_idle": "#2a2a37",
		"error": "#e82424", "mention": "#e82424", "link": "#7e9cd8", "code": "#7aa89f",
		"attachment": "#7fb4ca", "over_limit": "#e82424", "drop_fg": "#1f1f28",
		"priority_highest": "#e82424", "priority_high": "#ffa066",
		"priority_low": "#7e9cd8", "priority_lowest": "#727169",
		"type_bug": "#e82424", "type_story": "#98bb6c", "type_epic": "#957fb8",
		"type_subtask": "#727169", "type_other": "#7e9cd8", "type_alert": "#ffa066", "highlight": "#e6c384",
		"roadmap_done": "#98bb6c", "roadmap_todo": "#7e9cd8", "shade": "auto",
		"status_todo": "#727169", "status_progress": "#7e9cd8", "status_done": "#98bb6c",
	},
	"monokai": { // monokai
		"accent": "#66d9ef", "dim": "#75715e", "selection_fg": "#f8f8f2",
		"selection_bg": "#49483e", "selection_idle": "#3e3d32",
		"error": "#f92672", "mention": "#f92672", "link": "#66d9ef", "code": "#a6e22e",
		"attachment": "#66d9ef", "over_limit": "#f92672", "drop_fg": "#272822",
		"priority_highest": "#f92672", "priority_high": "#fd971f",
		"priority_low": "#66d9ef", "priority_lowest": "#75715e",
		"type_bug": "#f92672", "type_story": "#a6e22e", "type_epic": "#ae81ff",
		"type_subtask": "#75715e", "type_other": "#66d9ef", "type_alert": "#fd971f", "highlight": "#e6db74",
		"roadmap_done": "#a6e22e", "roadmap_todo": "#66d9ef", "shade": "auto",
		"status_todo": "#75715e", "status_progress": "#66d9ef", "status_done": "#a6e22e",
	},
	"catppuccin-latte": {
		"accent": "#1e66f5", "dim": "#8c8fa1", "selection_fg": "#4c4f69",
		"selection_bg": "#ccd0da", "selection_idle": "#dce0e8",
		"error": "#d20f39", "mention": "#d20f39", "link": "#1e66f5", "code": "#179299",
		"attachment": "#209fb5", "over_limit": "#d20f39", "drop_fg": "#eff1f5",
		"priority_highest": "#d20f39", "priority_high": "#fe640b",
		"priority_low": "#1e66f5", "priority_lowest": "#8c8fa1",
		"type_bug": "#d20f39", "type_story": "#40a02b", "type_epic": "#8839ef",
		"type_subtask": "#8c8fa1", "type_other": "#1e66f5", "type_alert": "#fe640b", "highlight": "#df8e1d",
		"roadmap_done": "#40a02b", "roadmap_todo": "#1e66f5", "shade": "auto",
		"status_todo": "#8c8fa1", "status_progress": "#1e66f5", "status_done": "#40a02b",
	},
	"catppuccin-frappe": {
		"accent": "#8caaee", "dim": "#737994", "selection_fg": "#c6d0f5",
		"selection_bg": "#51576d", "selection_idle": "#414559",
		"error": "#e78284", "mention": "#e78284", "link": "#8caaee", "code": "#81c8be",
		"attachment": "#85c1dc", "over_limit": "#e78284", "drop_fg": "#232634",
		"priority_highest": "#e78284", "priority_high": "#ef9f76",
		"priority_low": "#8caaee", "priority_lowest": "#737994",
		"type_bug": "#e78284", "type_story": "#a6d189", "type_epic": "#ca9ee6",
		"type_subtask": "#737994", "type_other": "#8caaee", "type_alert": "#ef9f76", "highlight": "#e5c890",
		"roadmap_done": "#a6d189", "roadmap_todo": "#8caaee", "shade": "auto",
		"status_todo": "#737994", "status_progress": "#8caaee", "status_done": "#a6d189",
	},
	"catppuccin-macchiato": {
		"accent": "#8aadf4", "dim": "#6e738d", "selection_fg": "#cad3f5",
		"selection_bg": "#494d64", "selection_idle": "#363a4f",
		"error": "#ed8796", "mention": "#ed8796", "link": "#8aadf4", "code": "#8bd5ca",
		"attachment": "#7dc4e4", "over_limit": "#ed8796", "drop_fg": "#181926",
		"priority_highest": "#ed8796", "priority_high": "#f5a97f",
		"priority_low": "#8aadf4", "priority_lowest": "#6e738d",
		"type_bug": "#ed8796", "type_story": "#a6da95", "type_epic": "#c6a0f6",
		"type_subtask": "#6e738d", "type_other": "#8aadf4", "type_alert": "#f5a97f", "highlight": "#eed49f",
		"roadmap_done": "#a6da95", "roadmap_todo": "#8aadf4", "shade": "auto",
		"status_todo": "#6e738d", "status_progress": "#8aadf4", "status_done": "#a6da95",
	},
	"rosepine-moon": {
		"accent": "#c4a7e7", "dim": "#6e6a86", "selection_fg": "#e0def4",
		"selection_bg": "#44415a", "selection_idle": "#2a283e",
		"error": "#eb6f92", "mention": "#eb6f92", "link": "#9ccfd8", "code": "#3e8fb0",
		"attachment": "#9ccfd8", "over_limit": "#eb6f92", "drop_fg": "#232136",
		"priority_highest": "#eb6f92", "priority_high": "#ea9a97",
		"priority_low": "#9ccfd8", "priority_lowest": "#6e6a86",
		"type_bug": "#eb6f92", "type_story": "#9ccfd8", "type_epic": "#c4a7e7",
		"type_subtask": "#6e6a86", "type_other": "#9ccfd8", "type_alert": "#f6c177", "highlight": "#f6c177",
		"roadmap_done": "#9ccfd8", "roadmap_todo": "#9ccfd8", "shade": "auto",
		"status_todo": "#6e6a86", "status_progress": "#9ccfd8", "status_done": "#9ccfd8",
	},
	"rosepine-dawn": {
		"accent": "#907aa9", "dim": "#8a869c", "selection_fg": "#575279",
		"selection_bg": "#dfdad9", "selection_idle": "#f2e9e1",
		"error": "#b4637a", "mention": "#b4637a", "link": "#56949f", "code": "#286983",
		"attachment": "#56949f", "over_limit": "#b4637a", "drop_fg": "#faf4ed",
		"priority_highest": "#b4637a", "priority_high": "#d7827e",
		"priority_low": "#56949f", "priority_lowest": "#8a869c",
		"type_bug": "#b4637a", "type_story": "#286983", "type_epic": "#907aa9",
		"type_subtask": "#8a869c", "type_other": "#56949f", "type_alert": "#ea9d34", "highlight": "#ea9d34",
		"roadmap_done": "#286983", "roadmap_todo": "#56949f", "shade": "auto",
		"status_todo": "#8a869c", "status_progress": "#56949f", "status_done": "#286983",
	},
	"tokyonight-storm": {
		"accent": "#7aa2f7", "dim": "#565f89", "selection_fg": "#c0caf5",
		"selection_bg": "#364a82", "selection_idle": "#2e3c64",
		"error": "#f7768e", "mention": "#f7768e", "link": "#7aa2f7", "code": "#7dcfff",
		"attachment": "#73daca", "over_limit": "#f7768e", "drop_fg": "#24283b",
		"priority_highest": "#f7768e", "priority_high": "#ff9e64",
		"priority_low": "#7aa2f7", "priority_lowest": "#565f89",
		"type_bug": "#f7768e", "type_story": "#9ece6a", "type_epic": "#bb9af7",
		"type_subtask": "#565f89", "type_other": "#7aa2f7", "type_alert": "#ff9e64", "highlight": "#e0af68",
		"roadmap_done": "#9ece6a", "roadmap_todo": "#7aa2f7", "shade": "auto",
		"status_todo": "#565f89", "status_progress": "#7aa2f7", "status_done": "#9ece6a",
	},
	"tokyonight-day": {
		"accent": "#2e7de9", "dim": "#7a82a8", "selection_fg": "#3760bf",
		"selection_bg": "#b7c1e3", "selection_idle": "#c4c8da",
		"error": "#f52a65", "mention": "#f52a65", "link": "#2e7de9", "code": "#007197",
		"attachment": "#118c74", "over_limit": "#f52a65", "drop_fg": "#e1e2e7",
		"priority_highest": "#f52a65", "priority_high": "#b15c00",
		"priority_low": "#2e7de9", "priority_lowest": "#7a82a8",
		"type_bug": "#f52a65", "type_story": "#587539", "type_epic": "#9854f1",
		"type_subtask": "#7a82a8", "type_other": "#2e7de9", "type_alert": "#b15c00", "highlight": "#8c6c3e",
		"roadmap_done": "#587539", "roadmap_todo": "#2e7de9", "shade": "auto",
		"status_todo": "#7a82a8", "status_progress": "#2e7de9", "status_done": "#587539",
	},
	"gruvbox-light": {
		"accent": "#076678", "dim": "#7c6f64", "selection_fg": "#3c3836",
		"selection_bg": "#d5c4a1", "selection_idle": "#ebdbb2",
		"error": "#9d0006", "mention": "#9d0006", "link": "#076678", "code": "#427b58",
		"attachment": "#427b58", "over_limit": "#9d0006", "drop_fg": "#fbf1c7",
		"priority_highest": "#9d0006", "priority_high": "#af3a03",
		"priority_low": "#076678", "priority_lowest": "#7c6f64",
		"type_bug": "#9d0006", "type_story": "#79740e", "type_epic": "#8f3f71",
		"type_subtask": "#7c6f64", "type_other": "#076678", "type_alert": "#af3a03", "highlight": "#b57614",
		"roadmap_done": "#79740e", "roadmap_todo": "#076678", "shade": "auto",
		"status_todo": "#7c6f64", "status_progress": "#076678", "status_done": "#79740e",
	},
	"solarized-dark": {
		"accent": "#268bd2", "dim": "#586e75", "selection_fg": "#93a1a1",
		"selection_bg": "#073642", "selection_idle": "#06303b",
		"error": "#dc322f", "mention": "#dc322f", "link": "#268bd2", "code": "#2aa198",
		"attachment": "#2aa198", "over_limit": "#dc322f", "drop_fg": "#002b36",
		"priority_highest": "#dc322f", "priority_high": "#cb4b16",
		"priority_low": "#268bd2", "priority_lowest": "#586e75",
		"type_bug": "#dc322f", "type_story": "#859900", "type_epic": "#6c71c4",
		"type_subtask": "#586e75", "type_other": "#268bd2", "type_alert": "#cb4b16", "highlight": "#b58900",
		"roadmap_done": "#859900", "roadmap_todo": "#268bd2", "shade": "auto",
		"status_todo": "#586e75", "status_progress": "#268bd2", "status_done": "#859900",
	},
	"solarized-light": {
		"accent": "#268bd2", "dim": "#93a1a1", "selection_fg": "#586e75",
		"selection_bg": "#eee8d5", "selection_idle": "#f5efdc",
		"error": "#dc322f", "mention": "#dc322f", "link": "#268bd2", "code": "#2aa198",
		"attachment": "#2aa198", "over_limit": "#dc322f", "drop_fg": "#fdf6e3",
		"priority_highest": "#dc322f", "priority_high": "#cb4b16",
		"priority_low": "#268bd2", "priority_lowest": "#93a1a1",
		"type_bug": "#dc322f", "type_story": "#859900", "type_epic": "#6c71c4",
		"type_subtask": "#93a1a1", "type_other": "#268bd2", "type_alert": "#cb4b16", "highlight": "#b58900",
		"roadmap_done": "#859900", "roadmap_todo": "#268bd2", "shade": "auto",
		"status_todo": "#93a1a1", "status_progress": "#268bd2", "status_done": "#859900",
	},
	"kanagawa-dragon": {
		"accent": "#8ba4b0", "dim": "#737c73", "selection_fg": "#c5c9c5",
		"selection_bg": "#393836", "selection_idle": "#282727",
		"error": "#c4746e", "mention": "#c4746e", "link": "#8ba4b0", "code": "#8ea4a2",
		"attachment": "#8ea4a2", "over_limit": "#c4746e", "drop_fg": "#181616",
		"priority_highest": "#c4746e", "priority_high": "#b6927b",
		"priority_low": "#8ba4b0", "priority_lowest": "#737c73",
		"type_bug": "#c4746e", "type_story": "#87a987", "type_epic": "#8992a7",
		"type_subtask": "#737c73", "type_other": "#8ba4b0", "type_alert": "#b6927b", "highlight": "#c4b28a",
		"roadmap_done": "#87a987", "roadmap_todo": "#8ba4b0", "shade": "auto",
		"status_todo": "#737c73", "status_progress": "#8ba4b0", "status_done": "#87a987",
	},
	"kanagawa-lotus": {
		"accent": "#4d699b", "dim": "#8a8980", "selection_fg": "#545464",
		"selection_bg": "#d5cea3", "selection_idle": "#e5ddb0",
		"error": "#c84053", "mention": "#c84053", "link": "#4d699b", "code": "#597b75",
		"attachment": "#597b75", "over_limit": "#c84053", "drop_fg": "#f2ecbc",
		"priority_highest": "#c84053", "priority_high": "#cc6d00",
		"priority_low": "#4d699b", "priority_lowest": "#8a8980",
		"type_bug": "#c84053", "type_story": "#6f894e", "type_epic": "#624c83",
		"type_subtask": "#8a8980", "type_other": "#4d699b", "type_alert": "#cc6d00", "highlight": "#77713f",
		"roadmap_done": "#6f894e", "roadmap_todo": "#4d699b", "shade": "auto",
		"status_todo": "#8a8980", "status_progress": "#4d699b", "status_done": "#6f894e",
	},
	"onelight": {
		"accent": "#4078f2", "dim": "#8a8c94", "selection_fg": "#383a42",
		"selection_bg": "#e5e5e6", "selection_idle": "#f0f0f1",
		"error": "#e45649", "mention": "#e45649", "link": "#4078f2", "code": "#0184bc",
		"attachment": "#0184bc", "over_limit": "#e45649", "drop_fg": "#fafafa",
		"priority_highest": "#e45649", "priority_high": "#986801",
		"priority_low": "#4078f2", "priority_lowest": "#8a8c94",
		"type_bug": "#e45649", "type_story": "#50a14f", "type_epic": "#a626a4",
		"type_subtask": "#8a8c94", "type_other": "#4078f2", "type_alert": "#c18401", "highlight": "#c18401",
		"roadmap_done": "#50a14f", "roadmap_todo": "#4078f2", "shade": "auto",
		"status_todo": "#8a8c94", "status_progress": "#4078f2", "status_done": "#50a14f",
	},
	"emico": { // emico.nl: yellow on near-black, the brand blue for selections
		"accent": "#f3ef7b", "dim": "#75777b", "selection_fg": "#f3f6f9",
		"selection_bg": "#003c6d", "selection_idle": "#292c2b",
		"error": "#ef5a6b", "mention": "#ef5a6b", "link": "#82caff", "code": "#2594c3",
		"attachment": "#82caff", "over_limit": "#ef5a6b", "drop_fg": "#171a18",
		"priority_highest": "#ef5a6b", "priority_high": "#f0a35e",
		"priority_low": "#82caff", "priority_lowest": "#75777b",
		"type_bug": "#ef5a6b", "type_story": "#8fd19e", "type_epic": "#b8a4f0",
		"type_subtask": "#75777b", "type_other": "#82caff", "type_alert": "#f0a35e", "highlight": "#f3ef7b",
		"roadmap_done": "#8fd19e", "roadmap_todo": "#82caff", "shade": "auto",
		"status_todo": "#75777b", "status_progress": "#82caff", "status_done": "#8fd19e",
	},
	"emico-light": { // emico.nl's light sections: the brand blue leads
		"accent": "#003c6d", "dim": "#75777b", "selection_fg": "#1e221f",
		"selection_bg": "#d3e0ec", "selection_idle": "#e9edf1",
		"error": "#c8364a", "mention": "#c8364a", "link": "#1f7fb0", "code": "#035a8d",
		"attachment": "#1f7fb0", "over_limit": "#c8364a", "drop_fg": "#f3f6f9",
		"priority_highest": "#c8364a", "priority_high": "#9a5c00",
		"priority_low": "#1f7fb0", "priority_lowest": "#75777b",
		"type_bug": "#c8364a", "type_story": "#2f7d46", "type_epic": "#6b4fbf",
		"type_subtask": "#75777b", "type_other": "#1f7fb0", "type_alert": "#9a5c00", "highlight": "#8a6d00",
		"roadmap_done": "#2f7d46", "roadmap_todo": "#1f7fb0", "shade": "auto",
		"status_todo": "#75777b", "status_progress": "#1f7fb0", "status_done": "#2f7d46",
	},
}

// curTheme is the theme applyTheme last set, for the priority marks and type icons.
var curTheme theme

func init() { applyTheme(defaultTheme()) }

var hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// themeFrom lays the config's colours over the defaults, reporting names
// and values it can't use.
func themeFrom(over map[string]string) (theme, []string) {
	th := defaultTheme()
	var warn []string
	if over["preset"] == "mono" || os.Getenv("NO_COLOR") != "" {
		th[monoKey] = "on"
	}
	if p, ok := over["preset"]; ok {
		if pt, ok := themePresets[p]; ok {
			maps.Copy(th, pt)
		} else {
			warn = append(warn, fmt.Sprintf(i18n.T("ui.theme: unknown preset %q"), p))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(over)) {
		v := over[name]
		if name == "preset" {
			continue
		}
		if _, ok := th[name]; !ok {
			warn = append(warn, fmt.Sprintf(i18n.T("ui.theme: unknown colour %q"), name))
			continue
		}
		if name == "shade" && (v == "auto" || v == "off") {
			th[name] = v
			continue
		}
		if n, err := strconv.Atoi(v); !(hexColor.MatchString(v) || err == nil && n >= 0 && n <= 255) {
			warn = append(warn, fmt.Sprintf(i18n.T("ui.theme.%s: %q is not 0–255 or #rrggbb"), name, v))
			continue
		}
		th[name] = v
	}
	return th, warn
}

// themeFallback is the colour each of these takes when the theme leaves it
// unset, as before it had its own.
var themeFallback = map[string]string{"status_todo": "dim", "status_progress": "roadmap_todo", "status_done": "roadmap_done", "type_alert": "highlight"}

// shadeStyle is the cards' faint background, used while shadeOn: the
// theme's shade colour, or with "auto" a step off the terminal's own
// background once it reports it (autoShade).
// barStyle, a step further, marks the bars that divide the screen: the
// board's title, lane heads, the panel's title and section headings, the
// status line.
var (
	shadeStyle, barStyle lipgloss.Style
	shadeOn              bool
)

// autoShade derives the shade from the terminal's background bg: a few
// percent toward white on a dark one, toward black on a light one. It does
// nothing unless the theme's shade is auto.
func autoShade(bg color.Color) {
	if curTheme["shade"] != "auto" || bg == nil {
		return
	}
	shadeStyle, barStyle, shadeOn = stepOff(bg, 0.06), stepOff(bg, 0.12), true
}

// stepOff is a background f of the way off bg: toward white on a dark one,
// toward black on a light one.
func stepOff(bg color.Color, f float64) lipgloss.Style {
	r, g, b, _ := bg.RGBA()
	c := [3]float64{float64(r >> 8), float64(g >> 8), float64(b >> 8)}
	dark := isDark(bg)
	for i := range c {
		if dark {
			c[i] += (255 - c[i]) * f
		} else {
			c[i] *= 1 - f
		}
	}
	return lipgloss.NewStyle().Background(lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", int(c[0]), int(c[1]), int(c[2]))))
}

// isDark is whether bg is a dark background.
func isDark(bg color.Color) bool {
	r, g, b, _ := bg.RGBA()
	return 0.299*float64(r>>8)+0.587*float64(g>>8)+0.114*float64(b>>8) < 128
}

// lightSelectionIdle replaces the default selection_idle, a dark grey
// behind the terminal's own text, on a light background.
const lightSelectionIdle = "253"

// adaptTheme fits the default's fixed greys to the terminal's background
// bg; a colour the config sets is kept.
func adaptTheme(bg color.Color) {
	if bg == nil || isDark(bg) || curTheme["selection_idle"] != defaultTheme()["selection_idle"] {
		return
	}
	th := maps.Clone(curTheme)
	th["selection_idle"] = lightSelectionIdle
	applyTheme(th)
}

// shade gives a line the faint background across width, keeping it through
// the line's own resets.
func shade(line string, width int) string { return paintBG(shadeStyle, line, width) }

// bar gives a line the bars' background across width.
func bar(line string, width int) string { return paintBG(barStyle, line, width) }

func paintBG(st lipgloss.Style, line string, width int) string {
	if !shadeOn {
		return line
	}
	return paintRow(st, line, width)
}

// paintRow gives line st's background across width, whatever the shading.
func paintRow(st lipgloss.Style, line string, width int) string {
	line = keepBG(line, ansiOpenSeq(st))
	if pad := width - visualWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return st.Render(line)
}

// monoKey marks a theme as mono (the preset, or NO_COLOR).
const monoKey = "\x00mono"

// monoTheme is whether the theme draws no colour.
var monoTheme bool

// applyTheme sets every themed colour and style.
func applyTheme(th theme) {
	clear(cardLinesMemo)
	if th[monoKey] != "" {
		applyMono(th)
		return
	}
	monoTheme = false
	th = maps.Clone(th)
	for name, from := range themeFallback {
		if th[name] == "" {
			th[name] = th[from]
		}
	}
	curTheme = th
	c := func(name string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(th[name])) }
	focusedColor, dimColor = lipgloss.Color(th["accent"]), lipgloss.Color(th["dim"])
	accent, dim := c("accent"), c("dim")

	selectedRow = c("selection_fg").Background(lipgloss.Color(th["selection_bg"]))
	shadeOn = false
	if v := th["shade"]; v != "auto" && v != "off" {
		shadeStyle, shadeOn = lipgloss.NewStyle().Background(lipgloss.Color(v)), true
		barStyle = shadeStyle
	}
	diffTreeSelStyle = lipgloss.NewStyle().Background(lipgloss.Color(th["selection_idle"]))
	scrollbarThumbStyle = accent
	mentionStyle = c("mention").Bold(true)
	attachmentStyle = c("attachment")
	statusStyle = dim

	jiraKeyStyle = accent.Bold(true)
	jiraDimStyle = dim
	jiraOverStyle = c("over_limit").Bold(true)
	jiraDropStyle = c("drop_fg").Bold(true).Background(focusedColor)
	jiraViewActive = accent.Bold(true)
	jiraGhostStyle = dim.Faint(true).Italic(true)
	jiraPinStyle = c("highlight")
	roadmapDoneStyle, roadmapTodoStyle, roadmapTodayStyle = c("roadmap_done"), c("roadmap_todo"), c("highlight")

	refKeyStyle = accent.Bold(true)
	refLabelStyle, refDimStyle = dim, dim
	onColour := func(bg string) lipgloss.Style {
		return c("drop_fg").Background(lipgloss.Color(th[bg])).Bold(true)
	}
	laneMark = map[string]lipgloss.Style{"new": c("status_todo"), "indeterminate": c("status_progress"), "done": c("status_done")}
	confettiStyle = c("status_done").Bold(true)
	statusLozenge = map[string]lipgloss.Style{
		"new":           c("selection_fg").Background(lipgloss.Color(th["selection_bg"])).Bold(true),
		"indeterminate": onColour("status_progress"),
		"done":          onColour("status_done"),
	}
	if th["status_todo"] != th["dim"] { // a to-do colour of its own
		statusLozenge["new"] = onColour("status_todo")
	}
	refErrStyle = c("error")

	mdCodeStyle, mdCodeBlockStyle = c("code"), c("code")
	mdCodeOpen = ansiOpenSeq(mdCodeStyle)
	mdFenceStyle, mdQuoteBarStyle = dim, dim
	mdLinkStyle = c("link").Underline(true)
	mdPanelStyles = map[string]lipgloss.Style{"info": c("link"), "note": c("type_epic"), "success": c("roadmap_done"),
		"warning": c("highlight"), "error": c("error")}
	mdDecisionStyle = c("roadmap_done")
}

// applyMono sets the styles to show state without colour: reverse for
// the cursor, a drop and a status, bold and underline for the rest.
func applyMono(th theme) {
	blank := theme{monoKey: "on"} // type and priority marks read by their glyphs
	for k := range th {
		if k != monoKey {
			blank[k] = ""
		}
	}
	curTheme, monoTheme = blank, true
	plain := lipgloss.NewStyle()
	bold, faint, rev := plain.Bold(true), plain.Faint(true), plain.Reverse(true)
	focusedColor, dimColor = lipgloss.NoColor{}, lipgloss.NoColor{}
	selectedRow = rev
	shadeOn = false
	diffTreeSelStyle = plain.Underline(true)
	scrollbarThumbStyle = bold
	mentionStyle, attachmentStyle, statusStyle = bold, plain.Underline(true), faint
	jiraKeyStyle, jiraDimStyle, jiraOverStyle = bold, faint, bold.Underline(true)
	jiraDropStyle, jiraViewActive = rev.Bold(true), bold.Underline(true)
	jiraGhostStyle, jiraPinStyle = faint.Italic(true), bold
	roadmapDoneStyle, roadmapTodoStyle, roadmapTodayStyle = faint, plain, bold
	refKeyStyle, refLabelStyle, refDimStyle = bold, faint, faint
	laneMark = map[string]lipgloss.Style{"new": faint, "indeterminate": bold, "done": plain}
	confettiStyle = plain
	statusLozenge = map[string]lipgloss.Style{"new": rev, "indeterminate": rev.Bold(true), "done": rev.Faint(true)}
	refErrStyle = bold.Underline(true)
	mdCodeStyle, mdCodeBlockStyle = plain, plain
	mdCodeOpen = ansiOpenSeq(mdCodeStyle)
	mdFenceStyle, mdQuoteBarStyle = faint, faint
	mdLinkStyle = plain.Underline(true)
	mdPanelStyles, mdDecisionStyle = map[string]lipgloss.Style{}, bold
}
