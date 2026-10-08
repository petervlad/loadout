// Package password hashes and checks account passwords with argon2id.
//
// Hashes are PHC strings ($argon2id$v=19$m=...,t=...,p=...$salt$hash) with a
// random salt, so the parameters travel with each hash and can be raised
// later without breaking old ones.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Length limits, in characters. The upper bound keeps one hash cheap enough
// that long inputs cannot be used to tie up the server.
const (
	MinLength = 12
	MaxLength = 128
)

// OWASP's argon2id recommendation: 19 MiB of memory, 2 passes, 1 lane.
const (
	memoryKiB = 19 * 1024
	passes    = 2
	lanes     = 1
	saltLen   = 16
	keyLen    = 32
)

// ErrMalformed means a stored hash is not a PHC string this package reads.
var ErrMalformed = errors.New("malformed password hash")

// WeakError says why a new password was refused. Its message is meant for
// the person choosing the password.
type WeakError struct {
	Reason string
}

func (e *WeakError) Error() string { return "password " + e.Reason }

// Check reports whether pw is acceptable as a new password for the account
// with the given email.
func Check(pw, email string) error {
	n := utf8.RuneCountInString(pw)
	switch {
	case !utf8.ValidString(pw):
		return &WeakError{Reason: "is not valid text"}
	case n < MinLength:
		return &WeakError{Reason: fmt.Sprintf("must be at least %d characters", MinLength)}
	case n > MaxLength:
		return &WeakError{Reason: fmt.Sprintf("must be at most %d characters", MaxLength)}
	}
	lower := strings.ToLower(pw)
	if _, ok := common[lower]; ok {
		return &WeakError{Reason: "is one of the most common passwords"}
	}
	distinct := map[rune]bool{}
	for _, r := range lower {
		distinct[r] = true
	}
	if len(distinct) < 5 {
		return &WeakError{Reason: "repeats too few different characters"}
	}
	if local, _, ok := strings.Cut(strings.ToLower(email), "@"); ok && (lower == strings.ToLower(email) || lower == local) {
		return &WeakError{Reason: "must not be your email address"}
	}
	return nil
}

// Hash returns the argon2id PHC string of pw with a fresh random salt.
func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, passes, memoryKiB, lanes, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, passes, lanes, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify reports whether pw matches the PHC string phc, comparing in
// constant time.
func Verify(pw, phc string) (bool, error) {
	p, err := parse(phc)
	if err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(pw), p.salt, p.passes, p.memory, p.lanes, uint32(len(p.key))) //nolint:gosec // G115: key length is checked in parse
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

// VerifyDummy costs what Verify costs for a hash made by Hash. Callers run
// it when there is no account to check, so a wrong email and a wrong
// password take the same time.
func VerifyDummy(pw string) {
	argon2.IDKey([]byte(pw), []byte("loadout-no-user!"), passes, memoryKiB, lanes, keyLen)
}

type params struct {
	memory, passes uint32
	lanes          uint8
	salt, key      []byte
}

// parse reads a PHC string, refusing parameters far outside what Hash
// writes so a tampered row cannot make one check take minutes.
func parse(phc string) (params, error) {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return params{}, ErrMalformed
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return params{}, ErrMalformed
	}
	var p params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.passes, &p.lanes); err != nil {
		return params{}, ErrMalformed
	}
	if p.memory < 8*1024 || p.memory > 256*1024 || p.passes < 1 || p.passes > 10 || p.lanes < 1 || p.lanes > 8 {
		return params{}, ErrMalformed
	}
	b64 := base64.RawStdEncoding
	var err error
	if p.salt, err = b64.DecodeString(parts[4]); err != nil || len(p.salt) < 8 {
		return params{}, ErrMalformed
	}
	if p.key, err = b64.DecodeString(parts[5]); err != nil || len(p.key) < 16 || len(p.key) > 64 {
		return params{}, ErrMalformed
	}
	return p, nil
}
