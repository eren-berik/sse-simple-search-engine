// Package domain holds the identity service's core types and errors.
package domain

import (
	"errors"
	"time"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

// User is a registered account. PasswordHash is a bcrypt hash; the plain password is never stored.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	Role         jwtauth.Role
	CreatedAt    time.Time
}

// RefreshToken is a stored refresh token. Only the SHA-256 hash of the token is persisted.
// Tokens issued from the same login share a FamilyID, so reuse of a rotated token can revoke the whole chain.
type RefreshToken struct {
	ID        string
	UserID    string
	FamilyID  string
	TokenHash []byte
	ExpiresAt time.Time
	RevokedAt *time.Time
	CreatedAt time.Time
}

var (
	ErrInvalidInput        = errors.New("invalid input")
	ErrNotFound            = errors.New("not found")
	ErrUsernameTaken       = errors.New("username is already taken")
	ErrInvalidCredentials  = errors.New("invalid username or password")
	ErrInvalidRefreshToken = errors.New("invalid or expired refresh token")
	// ErrRefreshTokenReused signals that an already rotated token was presented again,
	// which indicates theft; the token family is revoked.
	ErrRefreshTokenReused = errors.New("refresh token reuse detected")
)
