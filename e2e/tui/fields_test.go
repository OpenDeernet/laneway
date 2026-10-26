package tui

import "testing"

// panelOn opens the panel on the board's first card, DEMO-5.
func panelOn(t *testing.T) *term {
	t.Helper()
	s := start(t)
	s.keys("Enter")
	s.wait("DEMO-5  Story")
	return s
}

// TestAssignee: a picks someone; the card on the board shows them.
func TestAssignee(t *testing.T) {
	s := panelOn(t)
	s.keys("a")
	s.wait("type to filter")
	s.typ("Sam")
	s.gone("Assign to me") // the filter has answered
	s.keys("Enter")
	s.wait("Assignee: Sam Okafor")
	s.keys("Escape")
	s.waitLane(0, "SO Sam Okafor")
}

// TestPriority: p raises it a step; the card shows the arrow.
func TestPriority(t *testing.T) {
	s := panelOn(t)
	s.keys("p")
	s.wait("Lowest")
	s.keys("Up", "Enter")
	s.wait("Priority: High")
	s.keys("Escape")
	s.waitLane(0, "↑ due in 8d")
}

// TestPoints: P sets them; the lane's total follows.
func TestPoints(t *testing.T) {
	s := panelOn(t)
	s.keys("P")
	s.wait("Points:   ❯ 3")
	s.keys("BSpace")
	s.typ("8")
	s.keys("Enter")
	s.wait("Points:   8")
	s.keys("Escape")
	s.wait("To Do 3 · 11p")
}

// TestLabels: l adds one to those there.
func TestLabels(t *testing.T) {
	s := panelOn(t)
	s.keys("l")
	s.wait("Labels:   ❯ frontend")
	s.typ(" checkout")
	s.keys("Enter")
	s.wait("Labels:   frontend, checkout")
}

// TestDescription: E edits it in place, ctrl+s saves.
func TestDescription(t *testing.T) {
	s := panelOn(t)
	s.keys("E")
	s.wait("ctrl+s save")
	s.keys("Enter", "Enter")
	s.typ("Urgent: before the release.")
	s.keys("C-s")
	s.wait("Description  E edit")
	s.wait("Urgent: before the release.")
}

// TestCustomField: tab walks the fields to More, which opens the custom
// ones; Team is a select.
func TestCustomField(t *testing.T) {
	s := panelOn(t)
	s.wait("More fields: ▸ 5")
	for range 10 { // Summary … Labels, Updated, Created, then More
		s.keys("Tab")
	}
	s.keys("Enter")
	s.wait("Team *:      —")
	s.keys("Tab", "Tab", "Tab", "Tab", "Enter") // Components, Legacy ref, Parent, Team
	s.wait("Platform")
	s.typ("Plat")
	s.gone("Web")
	s.keys("Enter")
	s.wait("Team *:      Platform")
}
