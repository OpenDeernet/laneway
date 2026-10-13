package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestSealedStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sign-in.json")
	key := "0123456789abcdef0123456789abcdef"
	s, err := NewSealedStore(p, key, "ada")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save([]byte(`{"refresh":"secret-token"}`)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if bytes.Contains(raw, []byte("secret-token")) {
		t.Errorf("kept in the clear: %s", raw)
	}
	if got, _ := s.Load(); string(got) != `{"refresh":"secret-token"}` {
		t.Errorf("load %q", got)
	}
	for name, other := range map[string][2]string{"another key": {"x" + key, "ada"}, "another person": {key, "bob"}} {
		o, _ := NewSealedStore(p, other[0], other[1])
		if got, _ := o.Load(); len(got) != 0 {
			t.Errorf("%s opened it: %q", name, got)
		}
	}
	s.Save(nil)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("not removed: %v", err)
	}
}
