package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: OWASP minimum recommendation (19 MiB, t=2, p=1). At most
// maxConcurrentHashes run at once so a burst of logins cannot exhaust memory.
const (
	argonMemoryKiB      = 19 * 1024
	argonTime           = 2
	argonThreads        = 1
	argonKeyLen         = 32
	argonSaltLen        = 16
	maxConcurrentHashes = 4
)

var errBadHash = errors.New("malformed password hash")

type passwordHasher struct {
	sem   chan struct{}
	dummy string // verified against when the user does not exist, to equalise timing
}

func newPasswordHasher() *passwordHasher {
	h := &passwordHasher{sem: make(chan struct{}, maxConcurrentHashes)}
	dummy, err := h.hash(context.Background(), "dummy-password-for-timing")
	if err != nil {
		panic("argon2id self-test failed: " + err.Error())
	}
	h.dummy = dummy
	return h
}

func (h *passwordHasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("wait for password hasher: %w", ctx.Err())
	}
}

func (h *passwordHasher) release() { <-h.sem }

// hash returns a PHC-formatted argon2id hash.
func (h *passwordHasher) hash(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// verify checks password against a PHC argon2id hash in constant time.
func (h *passwordHasher) verify(ctx context.Context, password, encoded string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var mem, iters uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &iters, &threads); err != nil {
		return false, errBadHash
	}
	if mem == 0 || mem > 1<<20 || iters == 0 || iters > 16 || threads == 0 {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 || len(want) > 128 {
		return false, errBadHash
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := argon2.IDKey([]byte(password), salt, iters, mem, threads, uint32(len(want))) //nolint:gosec // len bounded above
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
