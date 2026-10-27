package ui

import (
	"os"
	"testing"
)

// TestMain runs the tests in colour even when the shell sets NO_COLOR: the
// theme and the code highlighter read it once, when the package loads, so a
// t.Setenv in a test comes too late. TestMonoTheme sets NO_COLOR itself.
func TestMain(m *testing.M) {
	os.Unsetenv("NO_COLOR")
	codeColorEnabled = true
	applyTheme(defaultTheme())
	os.Exit(m.Run())
}
