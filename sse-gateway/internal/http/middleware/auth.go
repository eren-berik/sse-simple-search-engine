// Package middleware holds the gateway's edge concerns: authentication and authorization,
// tiered rate limits, and resilience (circuit breakers and retries) for upstream calls.
// Services still verify tokens themselves; the gateway check rejects bad requests early.
package middleware

import (
	"errors"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/sse-gateway/pkg/jwtauth"
)

const claimsKey = "middleware.claims"

// Authenticate requires a valid bearer access token and stores its claims on the request.
// The Authorization header is forwarded unchanged so the service can verify it again.
func Authenticate(v *jwtauth.Verifier) fiber.Handler {
	return func(c *fiber.Ctx) error {
		raw, ok := bearerToken(c.Get(fiber.HeaderAuthorization))
		if !ok {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer`)
			return httpx.Error(c, fiber.StatusUnauthorized, "unauthorized", "missing bearer token")
		}
		claims, err := v.Verify(c.UserContext(), raw)
		if errors.Is(err, jwtauth.ErrKeysUnavailable) {
			return httpx.Error(c, fiber.StatusServiceUnavailable, "auth_unavailable", "authentication is temporarily unavailable")
		}
		if err != nil {
			c.Set(fiber.HeaderWWWAuthenticate, `Bearer error="invalid_token"`)
			return httpx.Error(c, fiber.StatusUnauthorized, "invalid_token", jwtauth.ErrInvalidToken.Error())
		}
		c.Locals(claimsKey, claims)
		return c.Next()
	}
}

// RequireRole allows the request only if the authenticated user has one of roles.
// It must run after Authenticate.
func RequireRole(roles ...jwtauth.Role) fiber.Handler {
	return func(c *fiber.Ctx) error {
		claims, ok := ClaimsFrom(c)
		if !ok || !slices.Contains(roles, claims.Role) {
			return httpx.Error(c, fiber.StatusForbidden, "forbidden", "insufficient permissions")
		}
		return c.Next()
	}
}

// WriteRequiresRole is RequireRole for state-changing methods only: reads stay open to every
// authenticated user, writes need one of roles.
func WriteRequiresRole(roles ...jwtauth.Role) fiber.Handler {
	require := RequireRole(roles...)
	return func(c *fiber.Ctx) error {
		if isSafeMethod(c.Method()) {
			return c.Next()
		}
		return require(c)
	}
}

// ClaimsFrom returns the claims stored by Authenticate.
func ClaimsFrom(c *fiber.Ctx) (*jwtauth.Claims, bool) {
	claims, ok := c.Locals(claimsKey).(*jwtauth.Claims)
	return claims, ok
}

func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", false
	}
	return strings.TrimSpace(token), true
}

// isSafeMethod reports whether method only reads (RFC 9110 §9.2.1).
func isSafeMethod(method string) bool {
	switch method {
	case fiber.MethodGet, fiber.MethodHead, fiber.MethodOptions:
		return true
	}
	return false
}
