package forms

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// KeySize is the length of the installation secrets key.
const KeySize = 32

const sealedPrefix = "enc:v1:"

// ErrUnsealable means a sealed value cannot be decrypted with this key: it
// was sealed with another key (e.g. a backup restored without the key file)
// or for another record.
var ErrUnsealable = errors.New("secret cannot be decrypted with the current key")

// LoadOrCreateKey reads the 32-byte key at path or creates it (mode 0600).
func LoadOrCreateKey(path string) ([]byte, error) {
	b, err := os.ReadFile(path) //nolint:gosec // path comes from STATE_DIR
	if err == nil {
		if len(b) != KeySize {
			return nil, fmt.Errorf("secrets key %s: expected %d bytes, found %d", path, KeySize, len(b))
		}
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("read secrets key: %w", err)
	}
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secrets key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // path comes from STATE_DIR
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return LoadOrCreateKey(path)
		}
		return nil, fmt.Errorf("create secrets key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write secrets key: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("sync secrets key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close secrets key: %w", err)
	}
	return key, nil
}

// Sealer encrypts secrets stored in the database (AES-256-GCM). The context
// string is authenticated, so a value sealed for one record does not open
// for another.
type Sealer struct {
	aead cipher.AEAD
	key  []byte
}

// NewSealer builds a Sealer from a KeySize-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secrets key must be %d bytes", KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("init cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init gcm: %w", err)
	}
	return &Sealer{aead: aead, key: append([]byte(nil), key...)}, nil
}

// Seal encrypts plaintext for context.
func (s *Sealer) Seal(plaintext, context string) string {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	ct := s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(context))
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(ct)
}

// Open decrypts a value produced by Seal for the same context.
func (s *Sealer) Open(sealed, context string) (string, error) {
	enc, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return "", ErrUnsealable
	}
	ct, err := base64.RawStdEncoding.DecodeString(enc)
	if err != nil || len(ct) < s.aead.NonceSize() {
		return "", ErrUnsealable
	}
	pt, err := s.aead.Open(nil, ct[:s.aead.NonceSize()], ct[s.aead.NonceSize():], []byte(context))
	if err != nil {
		return "", ErrUnsealable
	}
	return string(pt), nil
}

// Derive returns a hex subkey for purpose (HMAC-SHA256 of the key), used
// where a library wants a string secret, e.g. Altcha challenge signatures.
func (s *Sealer) Derive(purpose string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte("dropsite/" + purpose))
	return hex.EncodeToString(m.Sum(nil))
}
