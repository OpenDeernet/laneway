package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// server is the laneway web this app shows: one already running (started
// at login, say) or one it starts and stops.
type server struct {
	addr string
	cmd  *exec.Cmd
}

func (s *server) start() error {
	if v := s.version(); v != "" {
		log.Printf("using the laneway web %s already on %s", v, s.addr)
		return nil
	}
	exe, err := lanewayPath()
	if err != nil {
		return err
	}
	logf, err := logFile("desktop.log")
	if err != nil {
		return err
	}
	logPath := logf.Name()
	s.cmd = exec.Command(exe, "web", "-no-open", "-addr", s.addr)
	s.cmd.Stdout, s.cmd.Stderr = logf, logf
	s.cmd.Env = append(os.Environ(), "PATH="+loginPath())
	if err := s.cmd.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- s.cmd.Wait(); logf.Close() }()
	for range 100 {
		select {
		case err := <-exited:
			s.cmd = nil
			return fmt.Errorf("laneway web stopped (%v); see %s", err, logPath)
		case <-time.After(200 * time.Millisecond):
		}
		if v := s.version(); v != "" {
			log.Printf("started laneway web %s (%s) on %s", v, exe, s.addr)
			return nil
		}
	}
	return fmt.Errorf("laneway web did not answer on %s; see %s", s.addr, logPath)
}

// stop ends the laneway web this app started; one it found keeps running.
func (s *server) stop() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(os.Interrupt)
	time.AfterFunc(3*time.Second, func() { _ = s.cmd.Process.Kill() })
}

// version is that of the laneway web answering on addr, "" when none does.
func (s *server) version() string {
	c := http.Client{Timeout: time.Second}
	resp, err := c.Get("http://" + s.addr + "/api/session")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		Version string `json:"version"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&v) != nil {
		return ""
	}
	return v.Version
}

// logFile creates name in laneway's cache directory.
func logFile(name string) (*os.File, error) {
	path, err := logPath(name)
	if err != nil {
		return nil, err
	}
	return os.Create(path)
}

// logPath is name in laneway's cache directory, made if missing.
func logPath(name string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "laneway")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// appendLog opens name in laneway's cache directory to add to, emptied
// first when over 1 MB: what earlier starts logged stays, a hang included.
func appendLog(name string) (*os.File, error) {
	path, err := logPath(name)
	if err != nil {
		return nil, err
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_APPEND
	if fi, err := os.Stat(path); err == nil && fi.Size() > 1<<20 {
		flag |= os.O_TRUNC
	}
	return os.OpenFile(path, flag, 0o600)
}

// lanewayPath is the laneway binary in the app bundle, else on PATH.
func lanewayPath() (string, error) {
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "laneway")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("laneway"); err == nil {
		return p, nil
	}
	return "", errors.New("laneway is not next to this app nor on PATH")
}

// loginPath is the PATH of the user's shell: an app started from the Finder
// gets a bare one, without brew or the tools ui.actions and agents run.
func loginPath() string {
	sh := os.Getenv("SHELL")
	if sh == "" {
		return os.Getenv("PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, sh, "-l", "-i", "-c", `printf '\n@@%s@@' "$PATH"`).Output()
	if _, rest, ok := bytes.Cut(out, []byte("\n@@")); err == nil && ok {
		if p, _, ok := bytes.Cut(rest, []byte("@@")); ok && len(p) > 0 {
			return string(p)
		}
	}
	return os.Getenv("PATH")
}
