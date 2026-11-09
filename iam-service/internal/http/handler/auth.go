// Package handler contains the identity service's HTTP handlers.
package handler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/httpx"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/pkg/jwtauth"
)

// Authenticator is the auth use case consumed by AuthHandler.
type Authenticator interface {
	Register(ctx context.Context, in auth.RegisterInput) (auth.Session, error)
	Login(ctx context.Context, username, password string) (auth.Session, error)
	Refresh(ctx context.Context, refreshToken string) (auth.Session, error)
	Logout(ctx context.Context, refreshToken string) error
	User(ctx context.Context, id string) (domain.User, error)
}

type AuthHandler struct {
	svc Authenticator
	now func() time.Time
}

func NewAuthHandler(svc Authenticator) *AuthHandler {
	return &AuthHandler{svc: svc, now: time.Now}
}

type userDTO struct {
	ID        string       `json:"id"`
	Username  string       `json:"username"`
	Role      jwtauth.Role `json:"role"`
	CreatedAt time.Time    `json:"created_at"`
}

type sessionDTO struct {
	User             userDTO `json:"user"`
	TokenType        string  `json:"token_type"`
	AccessToken      string  `json:"access_token"`
	ExpiresIn        int     `json:"expires_in"`
	RefreshToken     string  `json:"refresh_token"`
	RefreshExpiresIn int     `json:"refresh_expires_in"`
}

type registerRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// Register handles POST /api/v1/auth/register.
func (h *AuthHandler) Register(c *fiber.Ctx) error {
	var req registerRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	s, err := h.svc.Register(c.UserContext(), auth.RegisterInput{Username: req.Username, Password: req.Password, Role: req.Role})
	if err != nil {
		return err
	}
	return httpx.Created(c, h.toSessionDTO(s))
}

// Login handles POST /api/v1/auth/login.
func (h *AuthHandler) Login(c *fiber.Ctx) error {
	var req loginRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	s, err := h.svc.Login(c.UserContext(), req.Username, req.Password)
	if err != nil {
		return err
	}
	return httpx.OK(c, h.toSessionDTO(s), nil)
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *AuthHandler) Refresh(c *fiber.Ctx) error {
	var req refreshRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if req.RefreshToken == "" {
		return fmt.Errorf("%w: refresh_token is required", domain.ErrInvalidInput)
	}
	s, err := h.svc.Refresh(c.UserContext(), req.RefreshToken)
	if err != nil {
		return err
	}
	return httpx.OK(c, h.toSessionDTO(s), nil)
}

// Logout handles POST /api/v1/auth/logout.
func (h *AuthHandler) Logout(c *fiber.Ctx) error {
	var req refreshRequest
	if err := parseBody(c, &req); err != nil {
		return err
	}
	if req.RefreshToken == "" {
		return fmt.Errorf("%w: refresh_token is required", domain.ErrInvalidInput)
	}
	if err := h.svc.Logout(c.UserContext(), req.RefreshToken); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// Me handles GET /api/v1/auth/me.
func (h *AuthHandler) Me(c *fiber.Ctx) error {
	claims, ok := jwtauth.ClaimsFrom(c)
	if !ok {
		return fiber.ErrUnauthorized
	}
	u, err := h.svc.User(c.UserContext(), claims.Subject)
	if err != nil {
		return err
	}
	return httpx.OK(c, toUserDTO(u), nil)
}

func (h *AuthHandler) toSessionDTO(s auth.Session) sessionDTO {
	now := h.now()
	return sessionDTO{
		User:             toUserDTO(s.User),
		TokenType:        "Bearer",
		AccessToken:      s.AccessToken,
		ExpiresIn:        secondsUntil(now, s.AccessTokenExpiresAt),
		RefreshToken:     s.RefreshToken,
		RefreshExpiresIn: secondsUntil(now, s.RefreshTokenExpiresAt),
	}
}

func secondsUntil(now, t time.Time) int {
	return int(t.Sub(now).Round(time.Second).Seconds())
}

func toUserDTO(u domain.User) userDTO {
	return userDTO{ID: u.ID, Username: u.Username, Role: u.Role, CreatedAt: u.CreatedAt}
}

func parseBody(c *fiber.Ctx, dst any) error {
	if err := c.BodyParser(dst); err != nil {
		return fmt.Errorf("%w: request body must be valid JSON", domain.ErrInvalidInput)
	}
	return nil
}

// ErrorMapper maps identity errors to HTTP responses.
func ErrorMapper(err error) (httpx.MappedError, bool) {
	switch {
	case errors.Is(err, domain.ErrInvalidInput):
		return httpx.MappedError{Status: fiber.StatusBadRequest, Code: "invalid_input", Message: err.Error()}, true
	case errors.Is(err, domain.ErrUsernameTaken):
		return httpx.MappedError{Status: fiber.StatusConflict, Code: "username_taken", Message: err.Error()}, true
	case errors.Is(err, domain.ErrInvalidCredentials):
		return httpx.MappedError{Status: fiber.StatusUnauthorized, Code: "invalid_credentials", Message: err.Error()}, true
	case errors.Is(err, domain.ErrInvalidRefreshToken):
		return httpx.MappedError{Status: fiber.StatusUnauthorized, Code: "invalid_refresh_token", Message: err.Error()}, true
	case errors.Is(err, domain.ErrNotFound):
		return httpx.MappedError{Status: fiber.StatusNotFound, Code: "not_found", Message: "resource not found"}, true
	}
	return httpx.MappedError{}, false
}
