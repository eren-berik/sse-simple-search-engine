package telemetry

import (
	"context"
	"fmt"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/auth"
	"github.com/eren-berik/sse-simple-search-engine/iam-service/internal/domain"
)

// fakeAuth fails every call with err.
type fakeAuth struct{ err error }

func (f fakeAuth) Register(context.Context, auth.RegisterInput) (auth.Session, error) {
	return auth.Session{}, f.err
}
func (f fakeAuth) Login(context.Context, string, string) (auth.Session, error) {
	return auth.Session{}, f.err
}
func (f fakeAuth) Refresh(context.Context, string) (auth.Session, error) {
	return auth.Session{}, f.err
}
func (f fakeAuth) Logout(context.Context, string) error              { return f.err }
func (f fakeAuth) User(context.Context, string) (domain.User, error) { return domain.User{}, f.err }

func TestAuthEventsByResult(t *testing.T) {
	ctx := context.Background()
	reg := prometheus.NewRegistry()
	counts := func(event, result string) float64 {
		families, _ := reg.Gather()
		for _, f := range families {
			for _, m := range f.GetMetric() {
				labels := map[string]string{}
				for _, l := range m.GetLabel() {
					labels[l.GetName()] = l.GetValue()
				}
				if labels["event"] == event && labels["result"] == result {
					return m.GetCounter().GetValue()
				}
			}
		}
		return 0
	}

	ok := InstrumentAuthenticator(reg, fakeAuth{})
	_, _ = ok.Login(ctx, "a", "b")
	_, _ = ok.Register(ctx, auth.RegisterInput{})
	_ = ok.Logout(ctx, "t")

	for _, tc := range []struct {
		err    error
		result string
	}{
		{domain.ErrInvalidCredentials, "invalid_credentials"},
		{fmt.Errorf("%w: %w", domain.ErrInvalidRefreshToken, domain.ErrRefreshTokenReused), "token_reused"},
		{domain.ErrInvalidRefreshToken, "invalid_refresh_token"},
		{domain.ErrUsernameTaken, "username_taken"},
		{fmt.Errorf("%w: short password", domain.ErrInvalidInput), "invalid_input"},
		{fmt.Errorf("db down"), "error"},
	} {
		assert.Equal(t, tc.result, result(tc.err))
	}

	assert.Equal(t, 1.0, counts("login", "success"))
	assert.Equal(t, 1.0, counts("register", "success"))
	assert.Equal(t, 1.0, counts("logout", "success"))

	failing := InstrumentAuthenticator(prometheus.NewRegistry(), fakeAuth{err: domain.ErrInvalidCredentials})
	_, err := failing.Login(ctx, "a", "b")
	assert.ErrorIs(t, err, domain.ErrInvalidCredentials, "errors are passed through")
	assert.Equal(t, 1.0, testutil.ToFloat64(failing.(*authenticator).events.WithLabelValues("login", "invalid_credentials")))
}
