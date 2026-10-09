package config

import (
	"encoding/base64"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// sign_in: laneway signs in to a laneway-server through its OAuth (jira's
// oauth.go) instead of email and api_token. The sign-in (a DPoP key and a
// refresh token) is kept in the OS keyring, or without one in a file only
// its owner reads.

// SignInLaneway is sign_in's value for a laneway-server sign-in.
const SignInLaneway = "laneway"

// LanewaySignIn reports whether the site signs in through laneway-server.
func (j JiraConfig) LanewaySignIn() bool { return strings.TrimSpace(j.SignIn) == SignInLaneway }

// CredentialStore is jira.CredentialStore: where a sign-in is kept.
type CredentialStore interface {
	Load() ([]byte, error)
	Save([]byte) error
}

// Credentials keeps the site's laneway sign-in; nil for an API-token
// site.
func (j JiraConfig) Credentials() CredentialStore {
	if !j.LanewaySignIn() {
		return nil
	}
	host := j.BaseURL
	if u, err := url.Parse(strings.TrimSpace(j.BaseURL)); err == nil && u.Host != "" {
		host = u.Host + strings.TrimRight(u.Path, "/")
	}
	if j.CredentialsFile != "" {
		return &SignInStore{at: j.CredentialsFile}
	}
	return &SignInStore{service: "laneway sign-in " + host, file: strings.NewReplacer("/", "_", ":", "_").Replace(host) + ".json"}
}

// SignInStore is where a site's sign-in lives: the keyring (service) or
// a file; at is a file of its own instead (laneway web for many people:
// one per person, never the server's keyring).
type SignInStore struct {
	service, file, at string
}

// keyring commands for the store; nil without a keyring tool. Tests swap
// it.
var signInKeyring = func(service string) (lookup, store, clear []string, encode func([]byte) string) {
	const account = "sign-in"
	b64 := func(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
	switch {
	case runtime.GOOS == "darwin" && have("security"):
		return []string{"security", "find-generic-password", "-a", account, "-s", service, "-w"},
			[]string{"security", "-i"},
			[]string{"security", "delete-generic-password", "-a", account, "-s", service}, b64
	case runtime.GOOS != "darwin" && have("secret-tool"):
		return []string{"secret-tool", "lookup", "service", service, "account", account},
			[]string{"secret-tool", "store", "--label=" + service, "service", service, "account", account},
			[]string{"secret-tool", "clear", "service", service, "account", account}, b64
	}
	return nil, nil, nil, nil
}

// keyring is signInKeyring for the store, none for a file of its own.
func (s *SignInStore) keyring() (lookup, store, clear []string, encode func([]byte) string) {
	if s.at != "" {
		return nil, nil, nil, nil
	}
	return signInKeyring(s.service)
}

func have(tool string) bool { _, err := exec.LookPath(tool); return err == nil }

// path is the file the sign-in is kept in without a keyring.
func (s *SignInStore) path() (string, error) {
	if s.at != "" {
		return s.at, nil
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "laneway", "sign-in", s.file), nil
}

// Load reads the sign-in: the keyring's, else the file's; nothing when
// there is none.
func (s *SignInStore) Load() ([]byte, error) {
	if lookup, _, _, _ := s.keyring(); lookup != nil {
		if out, err := exec.Command(lookup[0], lookup[1:]...).Output(); err == nil {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(out))); err == nil && len(raw) > 0 {
				return raw, nil
			}
		}
	}
	p, err := s.path()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return raw, err
}

// Save keeps the sign-in (nothing: removes it), in the keyring when there
// is one that works, else in the file.
func (s *SignInStore) Save(raw []byte) error {
	p, err := s.path()
	if err != nil {
		return err
	}
	lookup, store, clear, encode := s.keyring()
	if len(raw) == 0 {
		if lookup != nil {
			exec.Command(clear[0], clear[1:]...).Run()
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if store != nil {
		var cmd *exec.Cmd
		cmd = exec.Command(store[0], store[1:]...)
		cmd.Stdin = strings.NewReader(encode(raw))
		if store[0] == "security" {
			// -i reads the command on stdin, so the secret stays out of
			// ps; base64 and a host name need no escaping in its quotes.
			cmd.Stdin = strings.NewReader(`add-generic-password -U -a "sign-in" -s "` + s.service + `" -w "` + encode(raw) + "\"\n")
		}
		if cmd.Run() == nil {
			os.Remove(p) // an older copy without the keyring
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Lock holds the sign-in for one process (jira.Locker): a lock file next
// to it, made exclusively, so it works wherever the app runs. One older
// than lockStale was left by a process that died.
func (s *SignInStore) Lock() (func(), error) {
	p, err := s.path()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	lock := p + ".lock"
	for end := time.Now().Add(lockWait); ; {
		f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(lock) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > lockStale {
			os.Remove(lock)
			continue
		}
		if time.Now().After(end) {
			return nil, errors.New("another laneway is renewing the sign-in; try again")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// How long Lock waits for another process, and when a lock is stale.
var (
	lockWait  = 20 * time.Second
	lockStale = 30 * time.Second
)
