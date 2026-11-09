package jwtauth

import (
	"errors"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-service/pkg/httpx"
)

const claimsKey = "jwtauth.claims"

// Authenticate requires a valid bearer access token and stores its claims on the request.
func Authenticate(v *Verifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw, ok := bearerToken(c.Get(fiber.HeaderAuthorization))
		if !ok {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer`)
			return httpx.Error(c, fiber.StatusUnauthorized, "unauthorized", "missing bearer token")
		}
		claims, err := v.Verify(c.UserContext(), raw)
		if errors.Is(err, ErrKeysUnavailable) {
			return httpx.Error(c, fiber.StatusServiceUnavailable, "auth_unavailable", "authentication is temporarily unavailable")
		}
		if err != nil {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer error="invalid_token"`)
			return httpx.Error(c, fiber.StatusUnauthorized, "invalid_token", ErrInvalidToken.Error())
		}
		c.Locals(claimsKey, claims)
		return c.Next()
	}
}

// RequireRole allows the request only if the authenticated user has one of roles.
// It must run after Authenticate.
func RequireRole(roles ...Role) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFrom(c)
		if !ok || !slices.Contains(roles, claims.Role) {
			return httpx.Error(c, fiber.StatusForbidden, "forbidden", "insufficient permissions")
		}
		return c.Next()
	}
}

// ClaimsFrom returns the claims stored by Authenticate.
func ClaimsFrom(c *fiber.Ctx) (*Claims, bool) {
	claims, ok := c.Locals(claimsKey).(*Claims)
	return claims, ok
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}
