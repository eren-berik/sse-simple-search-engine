package jwtauth

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	// ErrInvalidToken covers malformed, expired, wrongly signed or wrongly addressed tokens.
	ErrInvalidToken = errors.New("invalid or expired token")
	// ErrUnknownKey means the token's kid is not among the known signing keys.
	ErrUnknownKey = errors.New("unknown signing key")
	// ErrKeysUnavailable means the signing keys could not be loaded (e.g. the issuer is down).
	ErrKeysUnavailable = errors.New("signing keys unavailable")
)

// KeySource resolves a key id to a public key.
type KeySource interface {
	Key(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// StaticKeySource is a fixed set of keys.
type StaticKeySource map[string]*rsa.PublicKey

func (s StaticKeySource) Key(_ context.Context, kid string) (*rsa.PublicKey, error) {
	if k, ok := s[kid]; ok {
		return k, nil
	}
	return nil, ErrUnknownKey
}

// ErrRevoked means the token's session was ended (logout, stolen refresh token) before the
// token expired. It wraps ErrInvalidToken, so callers treat it like any other invalid token.
var ErrRevoked = fmt.Errorf("%w: session revoked", ErrInvalidToken)

// RevocationChecker reports whether a session was revoked.
type RevocationChecker interface {
	IsRevoked(ctx context.Context, sessionID string) bool
}

// Verifier validates access tokens: RS256 only, known key, issuer, audience, expiry and, when
// configured, that the token's session has not been revoked.
type Verifier struct {
	keys        KeySource
	issuer      string
	audience    string
	leeway      time.Duration
	revocations RevocationChecker
}

func NewVerifier(keys KeySource, issuer, audience string) *Verifier {
	return &Verifier{keys: keys, issuer: issuer, audience: audience, leeway: 30 * time.Second}
}

// WithRevocations makes the verifier reject tokens of revoked sessions.
func (v *Verifier) WithRevocations(r RevocationChecker) *Verifier {
	v.revocations = r
	return v
}

// Verify parses and validates raw. Errors wrap ErrInvalidToken, or ErrKeysUnavailable when the
// keys could not be fetched, so callers can tell a bad token from an outage.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Claims, error) {
	claims := &Claims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, errors.New("missing kid header")
		}
		return v.keys.Key(ctx, kid)
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(v.leeway),
	)
	if errors.Is(err, ErrKeysUnavailable) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if claims.Subject == "" || !claims.Role.Valid() {
		return nil, fmt.Errorf("%w: missing subject or role", ErrInvalidToken)
	}
	if v.revocations != nil && claims.SessionID != "" && v.revocations.IsRevoked(ctx, claims.SessionID) {
		return nil, ErrRevoked
	}
	return claims, nil
}
