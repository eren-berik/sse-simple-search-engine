// Package jwtauth verifies RS256 access tokens issued by the identity service, using the
// public keys it publishes as JWKS, and provides Fiber middleware for authentication and roles.
package jwtauth

import (
	"fmt"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// Role is a user's authorization level. It is a string so it stays readable in tokens,
// JSON and the database, and adding a role needs no renumbering.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleViewer Role = "viewer"
)

// ParseRole validates a role name (case-insensitive).
func ParseRole(s string) (Role, error) {
	r := Role(strings.ToLower(strings.TrimSpace(s)))
	if !r.Valid() {
		return "", fmt.Errorf("unknown role %q (expected admin or viewer)", s)
	}
	return r, nil
}

func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleViewer
}

// Claims are the access token claims. Subject holds the user id; SessionID the login session
// (refresh token family) the token was issued for, so a whole session can be revoked at once.
type Claims struct {
	Username  string `json:"username"`
	Role      Role   `json:"role"`
	SessionID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}
