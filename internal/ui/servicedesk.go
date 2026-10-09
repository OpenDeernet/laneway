package ui

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cornedor/laneway/internal/i18n"
	"github.com/cornedor/laneway/internal/jira"
)

// An issue of a service desk project in the panel: its Request block (the
// request type, the customer, the SLAs' time left), and a comment that is
// an internal note unless ctrl+o makes it a reply to the customer.

// shownRequest is the panel issue as a desk's request, once loaded; nil
// for an issue of no desk.
func (m *Model) shownRequest() *jira.Request {
	if m.jiraIssue == nil || m.webLinksKey != m.jiraIssue.Key {
		return nil
	}
	return m.request
}

// renderRequest draws the Request block: type, customer, then each SLA's
// time left, red once breached.
func (m *Model) renderRequest(b *strings.Builder, width int) {
	r := m.shownRequest()
	if r == nil {
		return
	}
	b.WriteString(sectionHead(i18n.T("Request"), "", width))
	lw := panelLabelW()
	for _, s := range r.SLAs {
		lw = max(lw, len([]rune(s.Name))+2)
	}
	refField(b, i18n.T("Type"), r.Type, lw, false)
	who := r.Customer
	if r.CustomerEmail != "" {
		who += refDimStyle.Render(" <" + r.CustomerEmail + ">")
	}
	refField(b, i18n.T("Customer"), who, lw, false)
	for _, s := range r.SLAs {
		refField(b, s.Name, slaText(s), lw, false)
	}
}

// slaText is an SLA's state: the time left, paused, met, or breached in
// red.
func slaText(s jira.SLA) string {
	switch {
	case s.Done && s.Breached:
		return refErrStyle.Render(i18n.T("breached"))
	case s.Done:
		return i18n.T("met")
	case s.Breached:
		return refErrStyle.Render(i18n.Tf("%s · breached", s.Remaining))
	case s.Paused:
		return i18n.Tf("%s left · paused", s.Remaining)
	}
	return i18n.Tf("%s left", s.Remaining)
}

// deskComment is whether a comment on key goes to a service desk's
// request, as far as the panel has seen its project.
func (m *Model) deskComment(key string) bool { return m.desks[issueProject(key)] }

// deskVisibilities are who a comment on a desk's request can be for:
// an internal note (the default), the customer, then opts' roles and
// groups.
func deskVisibilities(opts []jira.Visibility) []jira.Visibility {
	out := []jira.Visibility{{Internal: true}, {Customer: true}}
	return append(out, slices.DeleteFunc(slices.Clone(opts), func(v jira.Visibility) bool { return v.Internal || v.Customer })...)
}

// deskCommentLine says who reads the comment being written on a desk's
// request: agents only, or the customer, in the warning colour.
func (m *Model) deskCommentLine() string {
	switch {
	case !m.deskComment(m.jiraCommentKey):
		return ""
	case m.jiraCommentVis.Customer:
		return refErrStyle.Bold(true).Render(i18n.T("reply to the customer: they read this · ctrl+o for an internal note"))
	case m.jiraCommentVis.Internal:
		return lipgloss.NewStyle().Foreground(dimColor).Render(i18n.T("internal note: only agents see it · ctrl+o to reply to the customer"))
	}
	return ""
}
