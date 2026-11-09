package auth

import (
	"errors"

	"golang.org/x/crypto/bcrypt"
)

// PasswordHasher hashes and checks passwords.
type PasswordHasher interface {
	Hash(password string) (string, error)
	// Compare returns nil when password matches hash.
	Compare(hash, password string) error
}

// BcryptHasher hashes passwords with bcrypt, which salts automatically and is deliberately slow.
type BcryptHasher struct {
	Cost int
}

func (h BcryptHasher) Hash(password string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(password), h.Cost)
	return string(b), err
}

func (h BcryptHasher) Compare(hash, password string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return errMismatch
	}
	return err
}

var errMismatch = errors.New("password mismatch")
