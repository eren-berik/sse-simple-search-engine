// Package telemetry records iam-service's domain metrics by wrapping the auth use case.
package telemetry

import (
	"context"
	"errors"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
)

// Authenticator is the auth use case as consumed by the HTTP handler.
type Authenticator interface {
	Register(ctx context.Context, in auth.RegisterInput) (auth.Session, error)
	Login(ctx context.Context, username, password string) (auth.Session, error)
	Refresh(ctx context.Context, refreshToken string) (auth.Session, error)
	Logout(ctx context.Context, refreshToken string) error
	User(ctx context.Context, id string) (domain.User, error)
}

// InstrumentAuthenticator counts auth events by outcome in auth_events_total{event,result}.
// A burst of invalid_credentials suggests password guessing; any token_reused means a refresh
// token was presented twice, i.e. probably stolen.
func InstrumentAuthenticator(reg prometheus.Registerer, a Authenticator) Authenticator {
	events := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "auth_events_total",
		Help: "Auth events (register, login, refresh, logout) by result.",
	}, []string{"event", "result"})
	reg.MustRegister(events)
	return &authenticator{Authenticator: a, events: events}
}

type authenticator struct {
	Authenticator
	events *prometheus.CounterVec
}

func (a *authenticator) Register(ctx context.Context, in auth.RegisterInput) (auth.Session, error) {
	s, err := a.Authenticator.Register(ctx, in)
	a.record("register", err)
	return s, err
}

func (a *authenticator) Login(ctx context.Context, username, password string) (auth.Session, error) {
	s, err := a.Authenticator.Login(ctx, username, password)
	a.record("login", err)
	return s, err
}

func (a *authenticator) Refresh(ctx context.Context, refreshToken string) (auth.Session, error) {
	s, err := a.Authenticator.Refresh(ctx, refreshToken)
	a.record("refresh", err)
	return s, err
}

func (a *authenticator) Logout(ctx context.Context, refreshToken string) error {
	err := a.Authenticator.Logout(ctx, refreshToken)
	a.record("logout", err)
	return err
}

func (a *authenticator) record(event string, err error) {
	a.events.WithLabelValues(event, result(err)).Inc()
}

func result(err error) string {
	switch {
	case err == nil:
		return "success"
	case errors.Is(err, domain.ErrRefreshTokenReused):
		return "token_reused"
	case errors.Is(err, domain.ErrInvalidCredentials):
		return "invalid_credentials"
	case errors.Is(err, domain.ErrInvalidRefreshToken):
		return "invalid_refresh_token"
	case errors.Is(err, domain.ErrUsernameTaken):
		return "username_taken"
	case errors.Is(err, domain.ErrInvalidInput):
		return "invalid_input"
	default:
		return "error"
	}
}
