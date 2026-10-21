package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoginPathDaemon: a shell whose profile leaves a daemon holding stdout
// still gives its PATH, without waiting on the daemon.
func TestLoginPathDaemon(t *testing.T) {
	sh := filepath.Join(t.TempDir(), "sh")
	os.WriteFile(sh, []byte("#!/bin/sh\nsleep 30 &\nprintf '\\n@@/daemon/bin@@'\n"), 0o755)
	t.Setenv("SHELL", sh)

	start := time.Now()
	if p := loginPath(); p != "/daemon/bin" {
		t.Errorf("PATH %q", p)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s", d)
	}
}
