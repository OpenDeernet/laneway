package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// SealedStore keeps a sign-in in a file of its own, encrypted (AES-GCM)
// with a key only the server's environment holds, and bound to whose it
// is: a copy of the data directory alone gives no tokens, and one
// person's file put in another's place doesn't open.
type SealedStore struct {
	file  *SignInStore
	aead  cipher.AEAD
	owner []byte
}

// NewSealedStore is the sign-in at path, sealed with a key derived from
// secret, for whose.
func NewSealedStore(path, secret, whose string) (*SealedStore, error) {
	if secret == "" {
		return nil, errors.New("no secret to seal the sign-in with")
	}
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, "laneway web sign-in", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &SealedStore{file: &SignInStore{at: path}, aead: aead, owner: []byte(whose)}, nil
}

// Load opens the sign-in; one that doesn't open is none.
func (s *SealedStore) Load() ([]byte, error) {
	raw, err := s.file.Load()
	if err != nil || len(raw) == 0 {
		return nil, err
	}
	n := s.aead.NonceSize()
	if len(raw) < n {
		return nil, nil
	}
	out, err := s.aead.Open(nil, raw[:n], raw[n:], s.owner)
	if err != nil {
		return nil, nil
	}
	return out, nil
}

// Save seals and keeps raw (nothing: removes it).
func (s *SealedStore) Save(raw []byte) error {
	if len(raw) == 0 {
		return s.file.Save(nil)
	}
	nonce := make([]byte, s.aead.NonceSize())
	rand.Read(nonce)
	return s.file.Save(s.aead.Seal(nonce, nonce, raw, s.owner))
}

// Lock is the file's lock (jira.Locker).
func (s *SealedStore) Lock() (func(), error) { return s.file.Lock() }
