// Package jwtauthtest issues access tokens for tests, standing in for the identity service.
package jwtauthtest

import (
	"crypto/rand"
	"crypto/rsa"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
)

const (
	Issuer   = "iam-service"
	Audience = "sse"
)

var (
	keyOnce sync.Once
	key     *rsa.PrivateKey
	keyErr  error
)

// TokenIssuer signs tokens with a key shared by all tests in the process.
type TokenIssuer struct {
	key *rsa.PrivateKey
	kid string
}

func New(t testing.TB) *TokenIssuer {
	t.Helper()
	keyOnce.Do(func() { key, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	if keyErr != nil {
		t.Fatalf("generate key: %v", keyErr)
	}
	return &TokenIssuer{key: key, kid: jwtauth.KeyID(&key.PublicKey)}
}

// Token returns a valid access token for a user with role.
func (i *TokenIssuer) Token(t testing.TB, role jwtauth.Role) string {
	t.Helper()
	return i.TokenWithExpiry(t, role, time.Now().Add(15*time.Minute))
}

func (i *TokenIssuer) TokenWithExpiry(t testing.TB, role jwtauth.Role, exp time.Time) string {
	t.Helper()
	return i.sign(t, "user-"+string(role), role, exp)
}

// TokenFor returns a valid access token for the user with id subject.
func (i *TokenIssuer) TokenFor(t testing.TB, subject string, role jwtauth.Role) string {
	t.Helper()
	return i.sign(t, subject, role, time.Now().Add(15*time.Minute))
}

func (i *TokenIssuer) sign(t testing.TB, subject string, role jwtauth.Role, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwtauth.Claims{
		Username: "test-" + string(role),
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    Issuer,
			Subject:   subject,
			Audience:  jwt.ClaimStrings{Audience},
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	})
	tok.Header["kid"] = i.kid
	signed, err := tok.SignedString(i.key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// Keys returns the verification keys.
func (i *TokenIssuer) Keys() jwtauth.StaticKeySource {
	return jwtauth.StaticKeySource{i.kid: &i.key.PublicKey}
}

// JWKS returns the key set as the identity service would publish it.
func (i *TokenIssuer) JWKS() jwtauth.JWKSet {
	return jwtauth.JWKSet{Keys: []jwtauth.JWK{jwtauth.NewJWK(&i.key.PublicKey, i.kid)}}
}

// Verifier returns a verifier configured for this issuer.
func (i *TokenIssuer) Verifier() *jwtauth.Verifier {
	return jwtauth.NewVerifier(i.Keys(), Issuer, Audience)
}
