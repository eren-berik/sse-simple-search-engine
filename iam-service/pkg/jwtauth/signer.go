package jwtauth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type SignerConfig struct {
	Key      *rsa.PrivateKey
	Issuer   string
	Audience []string
	TTL      time.Duration
	Now      func() time.Time
}

// Signer issues access tokens. It is the only holder of the private key.
type Signer struct {
	cfg SignerConfig
	kid string
}

func NewSigner(cfg SignerConfig) *Signer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Signer{cfg: cfg, kid: KeyID(&cfg.Key.PublicKey)}
}

// Sign issues an access token for the user's session and returns it with its expiry.
func (s *Signer) Sign(subject, username string, role Role, sessionID string) (string, time.Time, error) {
	now := s.cfg.Now()
	exp := now.Add(s.cfg.TTL)
	jti := make([]byte, 16)
	if _, err := rand.Read(jti); err != nil {
		return "", time.Time{}, fmt.Errorf("generate token id: %w", err)
	}

	claims := Claims{
		Username:  username,
		Role:      role,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.cfg.Issuer,
			Subject:   subject,
			Audience:  s.cfg.Audience,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
			ID:        hex.EncodeToString(jti),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = s.kid
	signed, err := token.SignedString(s.cfg.Key)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, exp, nil
}

// JWKS returns the public key set to publish for verifiers.
func (s *Signer) JWKS() JWKSet {
	return JWKSet{Keys: []JWK{NewJWK(&s.cfg.Key.PublicKey, s.kid)}}
}

// KeySource returns the signer's own public key, for verifying tokens inside the issuing service.
func (s *Signer) KeySource() StaticKeySource {
	return StaticKeySource{s.kid: &s.cfg.Key.PublicKey}
}
