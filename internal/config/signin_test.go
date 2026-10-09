package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSignInFileStore(t *testing.T) {
	t.Setenv("PATH", "") // no keyring tool
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if (JiraConfig{BaseURL: "https://lw.example"}).Credentials() != nil {
		t.Error("a token site has a sign-in store")
	}
	j := JiraConfig{BaseURL: "https://lw.example:8443/sub/", SignIn: SignInLaneway}
	st := j.Credentials()
	if raw, err := st.Load(); err != nil || raw != nil {
		t.Errorf("empty store: %q %v", raw, err)
	}
	if err := st.Save([]byte(`{"refresh":"lwr_1"}`)); err != nil {
		t.Fatal(err)
	}
	p, _ := st.(*SignInStore).path()
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 || filepath.Base(p) != "lw.example_8443_sub.json" {
		t.Errorf("file %s: %v %v", p, fi, err)
	}
	if fi, _ := os.Stat(filepath.Dir(p)); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
	if raw, _ := st.Load(); string(raw) != `{"refresh":"lwr_1"}` {
		t.Errorf("load %q", raw)
	}
	if err := st.Save(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("sign-out left the file: %v", err)
	}
	if err := (JiraConfig{BaseURL: "https://lw.example", SignIn: "sso"}).Check("jira"); err == nil {
		t.Error("an unknown sign_in passed the check")
	}
	if err := j.Check("jira"); err != nil {
		t.Errorf("laneway sign-in without email and token: %v", err)
	}
}

func TestSignInLock(t *testing.T) {
	t.Setenv("PATH", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	ow, os_ := lockWait, lockStale
	lockWait, lockStale = 200*time.Millisecond, time.Hour
	defer func() { lockWait, lockStale = ow, os_ }()
	st := JiraConfig{BaseURL: "https://lw.example", SignIn: SignInLaneway}.Credentials().(*SignInStore)
	unlock, err := st.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Lock(); err == nil {
		t.Fatal("a second lock while held")
	}
	unlock()
	unlock2, err := st.Lock()
	if err != nil {
		t.Fatalf("lock after unlock: %v", err)
	}
	// A lock its process left behind long ago is taken over.
	lockStale = 0
	if unlock3, err := st.Lock(); err != nil {
		t.Errorf("stale lock not taken over: %v", err)
	} else {
		unlock3()
	}
	unlock2()
}
