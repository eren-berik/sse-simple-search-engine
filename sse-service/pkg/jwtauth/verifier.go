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

// Verifier validates access tokens: RS256 only, known key, issuer, audience and expiry.
type Verifier struct {
	keys     KeySource
	issuer   string
	audience string
	leeway   time.Duration
}

func NewVerifier(keys KeySource, issuer, audience string) *Verifier {
	return &Verifier{keys: keys, issuer: issuer, audience: audience, leeway: 30 * time.Second}
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
	return claims, nil
}
